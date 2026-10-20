package dms

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"

	"papergo/internal/dal/taxonomy"
)

// Term set import reads the SharePoint term set CSV format: the columns
// "Term Set Name", "Term Set Description", "LCID", "Available for Tagging",
// "Term Description" and "Level 1 Term" to "Level 7 Term". A row with a term
// set name starts that set; each row adds the path of terms in its level
// columns. Import is additive: existing sets of the group (by name) and
// existing terms (by name under the same parent) are reused, new sets are
// closed, and a term's description and availability apply when the import
// creates it. "Available for Tagging" FALSE imports the term deprecated.

const (
	MaxTermImportBytes = 1 << 20
	maxTermImportRows  = 10000
	termImportLevels   = 7
)

type TermImportResult struct {
	TermSets     []TermSetView `json:"term_sets"`
	SetsCreated  int           `json:"sets_created"`
	TermsCreated int           `json:"terms_created"`
}

var nonKey = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Service) ImportTerms(ctx context.Context, subject, groupID string, body io.Reader) (out TermImportResult, err error) {
	records, err := readTermCSV(body)
	if err != nil {
		return out, err
	}
	err = s.write(ctx, func(t *Service) error {
		out = TermImportResult{TermSets: []TermSetView{}}
		g, e := t.termGroup(ctx, subject, groupID, "manage")
		if e != nil {
			return e
		}
		if g.System {
			return invalid("the system term group holds only the keywords set")
		}
		head := map[string]int{}
		for i, name := range records[0] {
			head[strings.ToLower(strings.TrimSpace(name))] = i
		}
		column := func(row []string, name string) string {
			if i, ok := head[name]; ok && i < len(row) {
				return strings.TrimSpace(row[i])
			}
			return ""
		}
		if _, ok := head["term set name"]; !ok {
			return invalid("the CSV needs a Term Set Name column")
		}
		if _, ok := head["level 1 term"]; !ok {
			return invalid("the CSV needs a Level 1 Term column")
		}
		var set *taxonomy.Set
		// children maps a set's active terms by parent id and normalized name.
		var children map[string]taxonomy.Term
		seen := map[string]bool{}
		for n, row := range records[1:] {
			line := "row " + strconv.Itoa(n+2) + ": "
			if name := column(row, "term set name"); name != "" {
				if e = validateName(name); e != nil {
					return invalid(line + e.Error())
				}
				imported, created, e := t.importSet(ctx, g, name, column(row, "term set description"))
				if e != nil {
					return e
				}
				set = &imported
				if created {
					out.SetsCreated++
				}
				if !seen[set.ID] {
					seen[set.ID] = true
					out.TermSets = append(out.TermSets, viewTermSet(imported))
				}
				terms, e := t.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{SetID: set.ID, Unmerged: true})
				if e != nil {
					return e
				}
				children = map[string]taxonomy.Term{}
				for _, v := range terms {
					children[termChildKey(v.ParentID, v.NormalizedName)] = v
				}
			}
			var parent *taxonomy.Term
			for level := 1; level <= termImportLevels; level++ {
				name := column(row, "level "+strconv.Itoa(level)+" term")
				if name == "" {
					for deeper := level + 1; deeper <= termImportLevels; deeper++ {
						if column(row, "level "+strconv.Itoa(deeper)+" term") != "" {
							return invalid(line + "term levels must not have gaps")
						}
					}
					break
				}
				if set == nil {
					return invalid(line + "terms need a term set name on this or an earlier row")
				}
				if e = validateName(name); e != nil {
					return invalid(line + e.Error())
				}
				parentID := ""
				if parent != nil {
					parentID = parent.ID
				}
				key := termChildKey(parentID, strings.ToLower(name))
				v, ok := children[key]
				if !ok {
					in := TermInput{Name: name, ParentID: optional(parentID), Labels: map[string]string{}, Synonyms: []string{}}
					if last := level == termImportLevels || column(row, "level "+strconv.Itoa(level+1)+" term") == ""; last {
						in.Description = column(row, "term description")
						in.Deprecated = strings.EqualFold(column(row, "available for tagging"), "false")
						if e = validateDescription(in.Description); e != nil {
							return invalid(line + e.Error())
						}
					}
					if parent != nil && parent.Deprecated {
						return invalid(line + "cannot add terms below the deprecated term " + parent.Name)
					}
					if v, e = t.insertTerm(ctx, set.ID, in); e != nil {
						return e
					}
					children[key] = v
					out.TermsCreated++
				}
				parent = &v
			}
		}
		return t.auditTaxonomy(ctx, subject, "term.import", map[string]any{"term_group_id": groupID, "sets_created": out.SetsCreated, "terms_created": out.TermsCreated})
	})
	return
}
func termChildKey(parentID, name string) string { return parentID + "\x00" + name }
func readTermCSV(body io.Reader) ([][]string, error) {
	raw, err := io.ReadAll(io.LimitReader(body, MaxTermImportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxTermImportBytes {
		return nil, invalid("the CSV exceeds 1 MiB")
	}
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var records [][]string
	for {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, invalid("invalid CSV: " + err.Error())
		}
		if len(records) > maxTermImportRows {
			return nil, invalid("at most 10000 term rows")
		}
		records = append(records, row)
	}
	if len(records) < 1 {
		return nil, invalid("the CSV needs a header row")
	}
	return records, nil
}

// importSet finds the group's set called name or creates it closed, with a
// key derived from the name.
func (s *Service) importSet(ctx context.Context, g taxonomy.Group, name, description string) (taxonomy.Set, bool, error) {
	store := s.DAL.Taxonomy()
	sets, e := store.Sets(ctx, taxonomy.SetFilter{GroupID: g.ID, Name: name, Limit: 1})
	if e != nil {
		return taxonomy.Set{}, false, e
	}
	if len(sets) > 0 {
		return sets[0], false, nil
	}
	if e = validateDescription(description); e != nil {
		return taxonomy.Set{}, false, e
	}
	base := strings.Trim(nonKey.ReplaceAllString(strings.ToLower(name), "_"), "_")
	if base == "" || base[0] < 'a' || base[0] > 'z' {
		base = "set_" + base
	}
	if len(base) > 56 {
		base = strings.TrimRight(base[:56], "_")
	}
	key := base
	for i := 2; ; i++ {
		taken, e := store.Sets(ctx, taxonomy.SetFilter{Key: key, Limit: 1})
		if e != nil {
			return taxonomy.Set{}, false, e
		}
		if len(taken) == 0 {
			break
		}
		key = base + "_" + strconv.Itoa(i)
	}
	set, e := store.CreateSet(ctx, taxonomy.Set{GroupID: g.ID, Key: key, Name: name, Description: description})
	return set, e == nil, taxonomyError(e)
}

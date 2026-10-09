package dms

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"papergo/ent"
	"papergo/ent/term"
	"papergo/ent/termset"
	"regexp"
	"strconv"
	"strings"
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
	TermSets     []*ent.TermSet `json:"term_sets"`
	SetsCreated  int            `json:"sets_created"`
	TermsCreated int            `json:"terms_created"`
}

var nonKey = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Service) ImportTerms(ctx context.Context, subject, groupID string, body io.Reader) (out TermImportResult, err error) {
	records, err := readTermCSV(body)
	if err != nil {
		return out, err
	}
	err = s.write(ctx, func(t *Service) error {
		out = TermImportResult{TermSets: []*ent.TermSet{}}
		g, w, e := t.termGroup(ctx, subject, groupID, "manage")
		if e != nil {
			return e
		}
		if g.IsSystem {
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
		var set *ent.TermSet
		// children maps a set's active terms by parent id and normalized name.
		var children map[string]*ent.Term
		seen := map[string]bool{}
		for n, row := range records[1:] {
			line := "row " + strconv.Itoa(n+2) + ": "
			if name := column(row, "term set name"); name != "" {
				if e = validateName(name); e != nil {
					return invalid(line + e.Error())
				}
				created := false
				if set, created, e = t.importSet(ctx, g, name, column(row, "term set description")); e != nil {
					return e
				}
				if created {
					out.SetsCreated++
				}
				if !seen[set.ID] {
					seen[set.ID] = true
					out.TermSets = append(out.TermSets, set)
				}
				terms, e := t.Client.Term.Query().Where(term.TermSetIDEQ(set.ID), term.MergedIntoIDIsNil()).All(ctx)
				if e != nil {
					return e
				}
				children = map[string]*ent.Term{}
				for _, v := range terms {
					children[termChildKey(v.ParentID, v.NormalizedName)] = v
				}
			}
			var parent *ent.Term
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
				var parentID *string
				if parent != nil {
					parentID = &parent.ID
				}
				key := termChildKey(parentID, strings.ToLower(name))
				v := children[key]
				if v == nil {
					in := TermInput{Name: name, Labels: map[string]string{}, Synonyms: []string{}}
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
					if v, e = t.insertTerm(ctx, set, parent, in); e != nil {
						return e
					}
					children[key] = v
					out.TermsCreated++
				}
				parent = v
			}
		}
		return t.audit(ctx, subject, "term.import", w, map[string]any{"term_group_id": groupID, "sets_created": out.SetsCreated, "terms_created": out.TermsCreated})
	})
	return
}
func termChildKey(parentID *string, name string) string {
	if parentID == nil {
		return "\x00" + name
	}
	return *parentID + "\x00" + name
}
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
func (s *Service) importSet(ctx context.Context, g *ent.TermGroup, name, description string) (*ent.TermSet, bool, error) {
	set, e := s.Client.TermSet.Query().Where(termset.GroupIDEQ(g.ID), termset.NameEQ(name)).Only(ctx)
	if e == nil || !ent.IsNotFound(e) {
		return set, false, e
	}
	if e = validateDescription(description); e != nil {
		return nil, false, e
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
		taken, e := s.Client.TermSet.Query().Where(termset.WorkspaceIDEQ(g.WorkspaceID), termset.KeyEQ(key)).Exist(ctx)
		if e != nil {
			return nil, false, e
		}
		if !taken {
			break
		}
		key = base + "_" + strconv.Itoa(i)
	}
	set, e = s.Client.TermSet.Create().SetWorkspaceID(g.WorkspaceID).SetGroupID(g.ID).SetKey(key).SetName(name).SetDescription(description).Save(ctx)
	return set, e == nil, e
}

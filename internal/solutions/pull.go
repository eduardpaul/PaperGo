package solutions

import (
	"context"
	"encoding/json"
	"strings"

	"papergo/internal/apps"
	"papergo/internal/dal/catalog"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// Pull returns an installed solution as its files, as it is now: what was
// applied, with what people changed in PaperGo since. Names, labels, views,
// parameters and sources are read from the workspace; IDs become references
// again. Applying what Pull returns changes nothing.
func (s *Service) Pull(ctx context.Context, subject, workspaceID, key string) (Solution, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (Solution, error) {
		r, err := s.workspace(ctx, t, subject, workspaceID)
		if err != nil {
			return Solution{}, err
		}
		in, ok := r.Installations[key]
		if !ok {
			return Solution{}, dms.ErrNotFound
		}
		// A copy, so that what is read does not reach the installation.
		var m Manifest
		raw, _ := json.Marshal(in.Manifest)
		if err := json.Unmarshal(raw, &m); err != nil {
			return Solution{}, err
		}
		p := puller{ctx: ctx, t: t, subject: subject, in: &in, refs: map[string]string{}}
		for _, o := range in.Objects {
			switch o.Kind {
			case KindCollection:
				p.refs[o.ID] = RefCollection + o.Key
			case KindContentType:
				p.refs[o.ID] = RefType + o.Key
			}
		}
		types, err := t.CatalogContentTypes(ctx, subject)
		if err != nil {
			return Solution{}, err
		}
		for _, slot := range m.Collections {
			for _, typ := range types {
				if _, expected := slot.Expects[typ.Key]; expected {
					p.refs[typ.ID] = RefType + typ.Key
				}
			}
		}
		if err := p.schema(&m, types); err != nil {
			return Solution{}, err
		}
		if err := p.collections(&m); err != nil {
			return Solution{}, err
		}
		out := Solution{Manifest: m}
		if out.Workflows, err = p.workflows(); err != nil {
			return Solution{}, err
		}
		if out.Apps, err = p.apps(); err != nil {
			return Solution{}, err
		}
		return out, nil
	})
}

type puller struct {
	ctx     context.Context
	t       *dms.Service
	subject string
	in      *Installation
	// refs are the references the IDs of the installation stand for.
	refs map[string]string
}

func (p *puller) schema(m *Manifest, types []*catalog.ContentType) error {
	fields, err := p.t.CatalogFields(p.ctx, p.subject)
	if err != nil {
		return err
	}
	byID, keyOf := map[string]*catalog.FieldDefinition{}, map[string]string{}
	for _, f := range fields {
		byID[f.ID], keyOf[f.ID] = f, f.Key
	}
	for i := range m.Fields {
		o := p.in.object(KindField, m.Fields[i].Key)
		if o == nil || byID[o.ID] == nil {
			continue
		}
		live := byID[o.ID]
		f := &m.Fields[i]
		f.Label, f.Required, f.Indexed, f.Choices, f.Options = live.Label, live.Required, live.Indexed, live.Choices, live.Options
		if ref, ok := p.refs[f.Options.LookupContainerID]; ok {
			f.Options.LookupContainerID = ref
		}
	}
	typeByID := map[string]*catalog.ContentType{}
	for _, t := range types {
		typeByID[t.ID] = t
	}
	own := m.Key + "_"
	for i := range m.ContentTypes {
		o := p.in.object(KindContentType, m.ContentTypes[i].Key)
		if o == nil || typeByID[o.ID] == nil {
			continue
		}
		live := typeByID[o.ID]
		m.ContentTypes[i].Name = live.Name
		// Fields of the solution someone added to the type in PaperGo.
		for _, id := range live.FieldIds {
			if key := keyOf[id]; strings.HasPrefix(key, own) && !contains(m.ContentTypes[i].Fields, key) && p.in.object(KindField, key) != nil {
				m.ContentTypes[i].Fields = append(m.ContentTypes[i].Fields, key)
			}
		}
	}
	return nil
}

func (p *puller) collections(m *Manifest) error {
	for i := range m.Collections {
		slot := &m.Collections[i]
		o := p.in.object(KindCollection, slot.Key)
		if o == nil {
			continue
		}
		if o.Role == RoleOwns {
			if c, err := p.t.Get(p.ctx, p.subject, o.ID); err == nil {
				slot.Name, slot.WebDAVEnabled = c.Name, c.WebdavEnabled
			}
		}
		for j := range slot.Views {
			v := &slot.Views[j]
			vo := p.in.object(KindView, slot.Key+"/"+v.Key)
			if vo == nil {
				continue
			}
			live, err := p.t.View(p.ctx, p.subject, vo.ID)
			if err != nil {
				continue // deleted in PaperGo: applying brings it back as the solution says
			}
			stored := storedView(live)
			v.Name, v.Columns, v.Layout, v.IsDefault = live.Name, live.Columns, string(live.Layout), live.IsDefault
			v.Query, _ = p.unresolve(stored["query"], v.Query).(map[string]any)
			v.Settings, _ = stored["settings"].(map[string]any)
			if len(v.Settings) == 0 {
				v.Settings = nil
			}
		}
	}
	return nil
}

// unresolve returns a live JSON value with the installation's IDs as
// references again, and with $setting: references where the solution had
// one and the value is still the setting's.
func (p *puller) unresolve(live, template any) any {
	if ref, ok := template.(string); ok && strings.HasPrefix(ref, RefSetting) {
		if hash(live) == hash(p.in.Settings[strings.TrimPrefix(ref, RefSetting)]) {
			return ref
		}
	}
	switch v := live.(type) {
	case string:
		if ref, ok := p.refs[v]; ok {
			return ref
		}
		return v
	case []any:
		templates, _ := template.([]any)
		out := make([]any, len(v))
		for i, e := range v {
			var t any
			if i < len(templates) {
				t = templates[i]
			}
			out[i] = p.unresolve(e, t)
		}
		return out
	case map[string]any:
		templates, _ := template.(map[string]any)
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = p.unresolve(e, templates[k])
		}
		return out
	}
	return live
}

// plain is a value as JSON holds it, whatever Go types it had.
func plain(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

func (p *puller) workflows() ([]Workflow, error) {
	svc := &workflow.Service{DMS: p.t}
	var out []Workflow
	for _, o := range p.in.Objects {
		if o.Kind != KindWorkflow || o.Retired {
			continue
		}
		live, err := svc.Get(p.ctx, p.subject, o.ID)
		if err != nil {
			continue
		}
		c := p.in.Components[KindWorkflow+":"+o.Key]
		if c.File == "" {
			c.File, c.Source = WorkflowsDir+"/"+o.Key+".json", o.Key+".ts"
		}
		w := Workflow{Key: o.Key, Name: live.Name, Description: live.Description, Script: c.Source, State: live.State, File: c.File, Source: live.Source,
			AutomaticEnabled: &live.AutomaticEnabled, ManualEnabled: &live.ManualEnabled}
		if p.in.Paused {
			// A paused installation's workflows are off whatever the solution says.
			w.AutomaticEnabled = c.AutomaticEnabled
		}
		w.Parameters, _ = p.unresolve(plain(live.Parameters), plain(c.Parameters)).(map[string]any)
		out = append(out, w)
	}
	return out, nil
}

func (p *puller) apps() ([]App, error) {
	svc := &apps.Service{DMS: p.t}
	var out []App
	for _, o := range p.in.Objects {
		if o.Kind != KindApp || o.Retired {
			continue
		}
		live, err := svc.Get(p.ctx, p.subject, o.ID)
		if err != nil {
			continue
		}
		c := p.in.Components[KindApp+":"+o.Key]
		if c.File == "" {
			c.File, c.Source = AppsDir+"/"+o.Key+".json", o.Key+".tsx"
		}
		a := App{Key: o.Key, Name: live.Name, Description: live.Description, SourceFile: c.Source, State: string(live.State), File: c.File, Source: live.Source}
		a.Parameters, _ = p.unresolve(plain(live.Parameters), plain(c.Parameters)).(map[string]any)
		out = append(out, a)
	}
	return out, nil
}

package solutions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// client talks to a PaperGo server's REST API with a personal access token.
type client struct {
	base, token string
	http        *http.Client
}

// connection are the flags every command that talks to a server has.
type connection struct {
	url, token, tokenFile, workspace *string
}

func connect(flags *flag.FlagSet) connection {
	return connection{
		url:       flags.String("url", os.Getenv("PAPERGO_URL"), "The server's address, such as https://docs.example.com (PAPERGO_URL)"),
		token:     flags.String("token", os.Getenv("PAPERGO_TOKEN"), "A personal access token (PAPERGO_TOKEN)"),
		tokenFile: flags.String("token-file", os.Getenv("PAPERGO_TOKEN_FILE"), "A file holding the token (PAPERGO_TOKEN_FILE)"),
		workspace: flags.String("workspace", os.Getenv("PAPERGO_WORKSPACE"), "The workspace, by name or ID (PAPERGO_WORKSPACE)"),
	}
}

func (c connection) client() (*client, error) {
	if *c.url == "" {
		return nil, errors.New("name the server with --url or PAPERGO_URL")
	}
	token := *c.token
	if token == "" && *c.tokenFile != "" {
		raw, err := os.ReadFile(*c.tokenFile)
		if err != nil {
			return nil, err
		}
		token = strings.TrimSpace(string(raw))
	}
	if token == "" {
		return nil, errors.New("give a personal access token with --token, --token-file, PAPERGO_TOKEN or PAPERGO_TOKEN_FILE")
	}
	return &client{base: strings.TrimRight(*c.url, "/"), token: token, http: &http.Client{Timeout: 5 * time.Minute}}, nil
}

// do sends a request and reads its JSON answer into into. An answer with
// one of the accepted statuses is no error.
func (c *client) do(ctx context.Context, method, path string, body []byte, contentType string, into any, accepted ...int) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return res.StatusCode, err
	}
	ok := res.StatusCode == http.StatusOK
	for _, status := range accepted {
		ok = ok || res.StatusCode == status
	}
	// An accepted status other than 200 carries a result, such as a refused
	// apply; the same status with a problem is an error like any other.
	var problem struct {
		Detail string `json:"detail"`
		Title  string `json:"title"`
	}
	if res.StatusCode != http.StatusOK && json.Unmarshal(raw, &problem) == nil && problem.Title != "" {
		ok = false
	}
	if !ok {
		if problem.Detail != "" || problem.Title != "" {
			return res.StatusCode, fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, strings.TrimSpace(problem.Title+": "+problem.Detail))
		}
		return res.StatusCode, fmt.Errorf("%s %s: %d", method, path, res.StatusCode)
	}
	if into != nil {
		if err := json.Unmarshal(raw, into); err != nil {
			return res.StatusCode, fmt.Errorf("%s %s: the answer is not what a PaperGo server sends: %w", method, path, err)
		}
	}
	return res.StatusCode, nil
}

var looksLikeID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type named struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// find returns the ID of the entry of a listing with a name, or the name
// itself when it is an ID.
func (c *client) find(ctx context.Context, what, path, name string) (string, error) {
	if looksLikeID.MatchString(name) {
		return name, nil
	}
	var names []string
	for next := path; next != ""; {
		var page struct {
			Value []named `json:"value"`
			Next  string  `json:"@odata.nextLink"`
		}
		if _, err := c.do(ctx, http.MethodGet, next, nil, "", &page); err != nil {
			return "", err
		}
		for _, e := range page.Value {
			if strings.EqualFold(e.Name, name) {
				return e.ID, nil
			}
			names = append(names, e.Name)
		}
		next = ""
		if u, err := url.Parse(page.Next); err == nil && page.Next != "" {
			next = u.RequestURI()
		}
	}
	return "", fmt.Errorf("there is no %s %q; there are: %s", what, name, strings.Join(names, ", "))
}

func (c *client) workspace(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", errors.New("name the workspace with --workspace or PAPERGO_WORKSPACE")
	}
	return c.find(ctx, "workspace", "/v1/workspaces", name)
}

// repeated collects the values of a flag given more than once.
type repeated []string

func (p *repeated) String() string     { return strings.Join(*p, ",") }
func (p *repeated) Set(v string) error { *p = append(*p, v); return nil }

// each reads the values as key=value.
func (p repeated) each(what string, fn func(key, value string) error) error {
	for _, pair := range p {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return fmt.Errorf("--%s takes key=value, not %q", what, pair)
		}
		if err := fn(key, value); err != nil {
			return err
		}
	}
	return nil
}

// applyCommand plans or applies a solution's folder or ZIP on a server.
func applyCommand(ctx context.Context, command string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(out)
	conn := connect(flags)
	var set, unset, bind, decide repeated
	flags.Var(&set, "set", "A setting of the solution, as name=value; repeat for more")
	flags.Var(&unset, "unset", "A setting to return to the solution's default, by name; repeat for more")
	flags.Var(&bind, "bind", "An existing list or library for a slot, as slot=name or slot=ID, or slot= to unbind it; repeat for more")
	flags.Var(&decide, "decide", "What to do with an object edited in PaperGo that the solution changes too, as kind:key=keep or kind:key=replace")
	prune := flags.Bool("prune", false, "Delete the workflows, apps and views this version no longer declares")
	language := flags.String("language", "", "The language of the display names to install, of those the solution has; none for its own text")
	paused := flags.Bool("paused", false, "Keep the solution's workflows from starting on their own")
	resume := flags.Bool("resume", false, "Let the workflows of a paused solution start again")
	folder, rest := leading(args)
	if err := flags.Parse(rest); err != nil {
		return err
	}
	if folder == "" || flags.NArg() != 0 || (*paused && *resume) {
		return errors.New(usage)
	}
	files, err := Open(folder)
	if err != nil {
		return err
	}
	s, problems := Load(files)
	if problems = append(problems, Check(ctx, s)...); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(out, p)
		}
		return fmt.Errorf("%s has %d problem(s)", folder, len(problems))
	}
	packed, err := Pack(s)
	if err != nil {
		return err
	}
	c, err := conn.client()
	if err != nil {
		return err
	}
	workspaceID, err := c.workspace(ctx, *conn.workspace)
	if err != nil {
		return err
	}
	var o Options
	if err := set.each("set", func(name, value string) error {
		if o.Settings == nil {
			o.Settings = map[string]any{}
		}
		parsed, err := settingValue(s.Manifest.Settings, name, value)
		o.Settings[name] = parsed
		return err
	}); err != nil {
		return err
	}
	for _, name := range unset {
		if o.Settings == nil {
			o.Settings = map[string]any{}
		}
		o.Settings[name] = nil
	}
	if err := bind.each("bind", func(slot, name string) error {
		if o.Bindings == nil {
			o.Bindings = map[string]string{}
		}
		if name == "" {
			o.Bindings[slot] = "" // unbind
			return nil
		}
		id, err := c.find(ctx, "list or library", "/v1/resources/"+workspaceID+"/children", name)
		o.Bindings[slot] = id
		return err
	}); err != nil {
		return err
	}
	if err := decide.each("decide", func(object, decision string) error {
		if decision != DecisionKeep && decision != DecisionReplace {
			return fmt.Errorf("--decide %s takes keep or replace, not %q", object, decision)
		}
		if o.Decisions == nil {
			o.Decisions = map[string]string{}
		}
		o.Decisions[object] = decision
		return nil
	}); err != nil {
		return err
	}
	if *paused || *resume {
		o.Paused = paused
	}
	o.Prune, o.Language = *prune, *language
	options, err := json.Marshal(o)
	if err != nil {
		return err
	}
	var result Result
	path := "/v1/workspaces/" + workspaceID + "/solutions/" + command + "?options=" + url.QueryEscape(string(options))
	if _, err := c.do(ctx, http.MethodPost, path, packed, "application/zip", &result, http.StatusConflict); err != nil {
		return err
	}
	report(out, result, command == "plan")
	if len(result.Refusals) > 0 {
		return fmt.Errorf("%d refusal(s); nothing was applied", len(result.Refusals))
	}
	return nil
}

// settingValue reads the value of a --set as the solution's settings form
// types it: text as it was written, and anything else as JSON.
func settingValue(form map[string]any, name, value string) (any, error) {
	properties, _ := form["properties"].(map[string]any)
	setting, ok := properties[name].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("--set %s: the solution has no such setting; it has %s", name, orNone(sortedKeys(properties)))
	}
	kind, _ := setting["type"].(string)
	if kind == "string" {
		return value, nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		if kind == "" {
			return value, nil // untyped: text unless it reads as JSON
		}
		return nil, fmt.Errorf("--set %s=%s: the setting is a %s, written as JSON", name, value, kind)
	}
	return parsed, nil
}

// leading splits the positional argument commands start with from their flags.
func leading(args []string) (string, []string) {
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		return args[0], args[1:]
	}
	return "", args
}

// report prints what applying a solution did, or would do.
func report(out io.Writer, r Result, plan bool) {
	from := "first installation"
	if r.From != "" {
		from = "installed: " + r.From
	}
	if r.Language != "" {
		from += ", in " + r.Language
	}
	fmt.Fprintf(out, "%s %s (%s)\n", r.Solution, r.Version, from)
	for _, c := range r.Changes {
		fmt.Fprintf(out, "  %-7s %s %s", c.Action, strings.ReplaceAll(c.Kind, "_", " "), c.Key)
		if c.Detail != "" {
			fmt.Fprintf(out, ": %s", c.Detail)
		}
		fmt.Fprintln(out)
	}
	for _, name := range r.Customized {
		fmt.Fprintf(out, "  kept    %s, as it was edited in PaperGo\n", strings.Replace(name, ":", " ", 1))
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(out, "  warning %s\n", w)
	}
	for _, f := range r.Refusals {
		fmt.Fprintf(out, "  REFUSED %s %s: %s\n", strings.ReplaceAll(f.Kind, "_", " "), f.Key, f.Reason)
	}
	switch {
	case len(r.Refusals) > 0:
	case plan:
		fmt.Fprintf(out, "plan: %d change(s); nothing was changed\n", len(r.Changes))
	default:
		fmt.Fprintf(out, "applied: %d change(s)\n", len(r.Changes))
	}
}

// listCommand lists the solutions installed in a workspace.
func listCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(out)
	conn := connect(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	c, err := conn.client()
	if err != nil {
		return err
	}
	workspaceID, err := c.workspace(ctx, *conn.workspace)
	if err != nil {
		return err
	}
	var list struct {
		Value []Summary `json:"value"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/v1/workspaces/"+workspaceID+"/solutions", nil, "", &list); err != nil {
		return err
	}
	for _, s := range list.Value {
		paused := ""
		if s.Paused {
			paused = " (paused)"
		}
		fmt.Fprintf(out, "%s %s%s: %s, applied %s by %s\n", s.Key, s.Version, paused, s.Name, s.AppliedAt, s.AppliedBy)
	}
	if len(list.Value) == 0 {
		fmt.Fprintln(out, "no solution is installed")
	}
	return nil
}

// statusCommand checks an installed solution; problems are an error.
func statusCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(out)
	conn := connect(flags)
	key, rest := leading(args)
	if err := flags.Parse(rest); err != nil {
		return err
	}
	if key == "" || flags.NArg() != 0 {
		return errors.New(usage)
	}
	c, err := conn.client()
	if err != nil {
		return err
	}
	workspaceID, err := c.workspace(ctx, *conn.workspace)
	if err != nil {
		return err
	}
	var status Status
	if _, err := c.do(ctx, http.MethodGet, "/v1/workspaces/"+workspaceID+"/solutions/"+url.PathEscape(key)+"/status", nil, "", &status); err != nil {
		return err
	}
	if status.OK {
		fmt.Fprintf(out, "%s %s: has what it needs\n", status.Solution, status.Version)
		return nil
	}
	fmt.Fprintf(out, "%s %s needs attention:\n", status.Solution, status.Version)
	for _, p := range status.Problems {
		fmt.Fprintf(out, "  %s\n", p)
	}
	return fmt.Errorf("%d problem(s)", len(status.Problems))
}

// removeCommand uninstalls a solution from a workspace.
func removeCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("remove", flag.ContinueOnError)
	flags.SetOutput(out)
	conn := connect(flags)
	key, rest := leading(args)
	if err := flags.Parse(rest); err != nil {
		return err
	}
	if key == "" || flags.NArg() != 0 {
		return errors.New(usage)
	}
	c, err := conn.client()
	if err != nil {
		return err
	}
	workspaceID, err := c.workspace(ctx, *conn.workspace)
	if err != nil {
		return err
	}
	var result Result
	if _, err := c.do(ctx, http.MethodDelete, "/v1/workspaces/"+workspaceID+"/solutions/"+url.PathEscape(key), nil, "", &result); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %s\n", result.Solution, result.Version)
	for _, ch := range result.Changes {
		fmt.Fprintf(out, "  %-7s %s %s\n", ch.Action, strings.ReplaceAll(ch.Kind, "_", " "), ch.Key)
	}
	fmt.Fprintln(out, "removed; its lists, content types and fields stay with what they hold")
	return nil
}

// pullCommand writes an installation's files into a folder: the solution
// as it is now in the workspace.
func pullCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("pull", flag.ContinueOnError)
	flags.SetOutput(out)
	conn := connect(flags)
	target := flags.String("out", "", "The folder to write the solution's files into; its other files stay")
	key, rest := leading(args)
	if err := flags.Parse(rest); err != nil {
		return err
	}
	if key == "" || flags.NArg() != 0 || *target == "" {
		return errors.New(usage)
	}
	c, err := conn.client()
	if err != nil {
		return err
	}
	workspaceID, err := c.workspace(ctx, *conn.workspace)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/workspaces/"+workspaceID+"/solutions/"+url.PathEscape(key)+"/package", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, MaxPackageBytes+1))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("pull %s: %d %s", key, res.StatusCode, strings.TrimSpace(string(raw)))
	}
	files, err := OpenZIP(raw)
	if err != nil {
		return err
	}
	s, problems := Load(files)
	if len(problems) > 0 {
		return problems
	}
	written, err := Write(s, *target)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %s: %d file(s) written to %s\n", s.Manifest.Key, s.Manifest.Version, written, *target)
	return nil
}

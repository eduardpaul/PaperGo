package solutions

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const usage = `usage: papergo solution <command>
  pack <folder> [--out <file or folder>]       check a solution's folder and write its ZIP
  plan <folder or ZIP> --workspace <name>       show what applying it would change
  apply <folder or ZIP> --workspace <name>      apply it
       [--set name=value] [--unset name] [--bind slot=list] [--decide kind:key=keep|replace] [--language es] [--prune] [--paused | --resume]
  list --workspace <name>                       the solutions installed
  status <key> --workspace <name>               whether one has what it needs
  pull <key> --workspace <name> --out <folder>  write an installation's files, as it is now
  remove <key> --workspace <name>               uninstall one; its lists and their items stay
  schema [solution|workflow|app|locale]         the JSON Schemas of the files
plan, apply, list, status, pull and remove talk to a server: --url (PAPERGO_URL) and --token or --token-file (PAPERGO_TOKEN, PAPERGO_TOKEN_FILE)`

// Run runs a solution command: papergo solution <command>. A command asked
// for --help prints it and returns flag.ErrHelp.
func Run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "-h", "--help":
		fmt.Fprintln(out, usage)
		return flag.ErrHelp
	case "pack":
		return packCommand(ctx, args[1:], out)
	case "schema":
		return schemaCommand(args[1:], out)
	case "plan", "apply":
		return applyCommand(ctx, args[0], args[1:], out)
	case "list":
		return listCommand(ctx, args[1:], out)
	case "status":
		return statusCommand(ctx, args[1:], out)
	case "remove":
		return removeCommand(ctx, args[1:], out)
	case "pull":
		return pullCommand(ctx, args[1:], out)
	}
	return errors.New(usage)
}

// packCommand checks a solution's folder and writes it as <key>-<version>.zip.
// With problems it lists them all and writes nothing.
func packCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("pack", flag.ContinueOnError)
	flags.SetOutput(out)
	target := flags.String("out", ".", "File to write, or the folder to write <key>-<version>.zip into")
	// Flags may follow the folder, as in the usage line.
	var folder string
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		folder, args = args[0], args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if folder == "" && flags.NArg() == 1 {
		folder = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return errors.New(usage)
	}
	if folder == "" {
		return errors.New(usage)
	}
	files, err := Open(folder)
	if err != nil {
		return err
	}
	s, problems := Load(files)
	problems = append(problems, Check(ctx, s)...)
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(out, p)
		}
		return fmt.Errorf("%s has %d problem(s); nothing was written", folder, len(problems))
	}
	packed, err := Pack(s)
	if err != nil {
		return err
	}
	name := *target
	if info, err := os.Stat(name); err == nil && info.IsDir() {
		name = filepath.Join(name, s.FileName())
	}
	if err := os.WriteFile(name, packed, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %s: %d field(s), %d content type(s), %d collection(s), %d workflow(s), %d app(s)\n%s\n",
		s.Manifest.Key, s.Manifest.Version, len(s.Manifest.Fields), len(s.Manifest.ContentTypes), len(s.Manifest.Collections), len(s.Workflows), len(s.Apps), name)
	return nil
}

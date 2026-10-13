package buildinfo

import "fmt"

var Version = "development"
var Commit = "unknown"
var Date = "unknown"

func String() string { return fmt.Sprintf("PaperGo %s (commit %s, built %s)", Version, Commit, Date) }

// Command openapi writes the REST contract generated from internal/httpapi.
package main

import (
	"flag"
	"fmt"
	"os"
	"papergo/internal/httpapi"
)

func main() {
	out := flag.String("o", "api/openapi.json", "output file")
	flag.Parse()
	spec, err := httpapi.Spec()
	if err == nil {
		err = os.WriteFile(*out, spec, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

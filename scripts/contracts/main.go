// Command contracts runs the offline checks of the contracts aggregate
// (docs/WORKPACKAGES/WP-L1.md). The shell scripts in scripts/ call it.
//
//	go run ./scripts/contracts examples [-v] [DIR]   validate every example both ways
//	go run ./scripts/contracts index [-check]        write or check api/index.md
//	go run ./scripts/contracts openapi FILE...       each file parses as OpenAPI 3.1
//	go run ./scripts/contracts groups SYSTEM FILE    the 02 §3 groups exist in FILE
//
// Every subcommand prints what it checked, including a count of zero
// (LESSONS E-04: "0 checked" is a visible line, not a silent pass).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rootxkit/uspace-lab/internal/contracts"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: contracts examples ...")
		return 2
	}
	switch args[0] {
	case "examples":
		return examples(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", args[0])
	return 2
}

func examples(args []string) int {
	fs := flag.NewFlagSet("examples", flag.ContinueOnError)
	verbose := fs.Bool("v", false, "print every example and the reason each invalid one fails")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := filepath.Join("schemas", "common")
	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}
	rep, err := contracts.CheckExamples(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	valid, invalid := 0, 0
	for _, s := range rep.Schemas {
		valid += s.Valid
		invalid += s.Invalid
		status := "ok"
		for _, e := range s.Results {
			if e.Problem != "" {
				status = "FAIL"
			}
		}
		if len(s.Problems) > 0 {
			status = "FAIL"
		}
		fmt.Printf("%-4s %-22s %d valid, %d invalid\n", status, s.Name, s.Valid, s.Invalid)
		for _, p := range s.Problems {
			fmt.Printf("     problem: %s\n", p)
		}
		for _, e := range s.Results {
			rel, _ := filepath.Rel(root, e.File)
			switch {
			case e.Problem != "":
				fmt.Printf("     FAIL %s: %s\n", filepath.ToSlash(rel), e.Problem)
			case *verbose && e.WantValid:
				fmt.Printf("     ok   %s\n", filepath.ToSlash(rel))
			case *verbose:
				fmt.Printf("     ok   %s fails: %s\n", filepath.ToSlash(rel), e.Reason)
			}
		}
	}
	fmt.Printf("%d schemas, %d valid examples validated, %d invalid examples refused\n", len(rep.Schemas), valid, invalid)
	if rep.Failed() {
		return 1
	}
	return 0
}

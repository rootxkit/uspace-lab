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
	"bytes"
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
		fmt.Fprintln(os.Stderr, "usage: contracts examples|index|openapi|groups ...")
		return 2
	}
	switch args[0] {
	case "examples":
		return examples(args[1:])
	case "index":
		return index(args[1:])
	case "openapi":
		return openapi(args[1:])
	case "groups":
		return groups(args[1:])
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

func index(args []string) int {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	check := fs.Bool("check", false, "fail if api/index.md differs from what would be generated")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	got, err := contracts.RenderIndex(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if *check {
		cur, err := os.ReadFile(contracts.IndexPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		if !bytes.Equal(cur, []byte(got)) {
			fmt.Fprintf(os.Stderr, "%s is out of date: run go run ./scripts/contracts index\n", contracts.IndexPath)
			return 1
		}
		fmt.Printf("ok: %s is current\n", contracts.IndexPath)
		return 0
	}
	if err := os.WriteFile(contracts.IndexPath, []byte(got), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Printf("wrote %s\n", contracts.IndexPath)
	return 0
}

func openapi(args []string) int {
	rc := 0
	for _, f := range args {
		o, err := contracts.ReadOpenAPI(f)
		if err != nil {
			fmt.Printf("FAIL %s: %v\n", f, err)
			rc = 1
			continue
		}
		fmt.Printf("ok   %s: OpenAPI %s, info.version %s, %d paths\n", f, o.Version, o.InfoVersion, len(o.Paths))
	}
	fmt.Printf("%d OpenAPI files checked\n", len(args))
	return rc
}

func groups(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: contracts groups SYSTEM FILE")
		return 2
	}
	o, err := contracts.ReadOpenAPI(args[1])
	if err != nil {
		fmt.Printf("FAIL %s: %v\n", args[1], err)
		return 1
	}
	rc := 0
	gs := contracts.GroupsFor(args[0])
	for _, g := range gs {
		switch missing := g.Missing(o.Paths); {
		case g.Standard:
			fmt.Printf("skip %s (standard endpoints, defined by uas_standards)\n", g.Row)
		case len(missing) == 0:
			fmt.Printf("ok   %s\n", g.Row)
		case g.Optional:
			fmt.Printf("note %s: optional group absent: %v\n", g.Row, missing)
		default:
			fmt.Printf("FAIL %s: no path for %v\n", g.Row, missing)
			rc = 1
		}
	}
	fmt.Printf("%d endpoint groups of 02 §3 checked for %s\n", len(gs), args[0])
	return rc
}

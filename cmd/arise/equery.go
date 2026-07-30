package main

import (
	"fmt"
	"io"
	"os"

	"github.com/airencracken/arise/internal/equery"
	"github.com/airencracken/arise/internal/ingest"
)

func runEquery(args []string, dbPath, repoDir, vdbPath string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "equery: expected subcommand: belongs, files, uses, size, check, which\n")
		os.Exit(1)
	}
	subcmd := args[0]
	subArgs := args[1:]
	if subcmd == "--help" || subcmd == "-h" || subcmd == "help" {
		writeCommandHelp(os.Stdout, "equery")
		return
	}
	if isHelpRequest(subArgs) {
		if writeEquerySubcommandHelp(os.Stdout, subcmd) {
			return
		}
		fmt.Fprintf(os.Stderr, "equery: unknown subcommand %q\n", subcmd)
		os.Exit(1)
	}

	var arg string
	if len(subArgs) > 0 {
		arg = subArgs[0]
	}

	switch subcmd {
	case "belongs":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "equery belongs: missing file path argument\n")
			os.Exit(1)
		}
		pkg, err := equery.Belongs(vdbPath, arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery belongs: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(pkg)

	case "files":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "equery files: missing atom argument\n")
			os.Exit(1)
		}
		files, err := equery.Files(vdbPath, arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery files: %v\n", err)
			os.Exit(1)
		}
		for _, f := range files {
			fmt.Println(f)
		}

	case "uses":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "equery uses: missing atom argument\n")
			os.Exit(1)
		}
		db, err := ingest.OpenReadOnlyDB(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery uses: open db: %v\n", err)
			os.Exit(1)
		}
		iuse, active, err := equery.Uses(db, vdbPath, arg)
		db.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery uses: %v\n", err)
			os.Exit(1)
		}
		if iuse != "" {
			fmt.Printf("IUSE: %s\n", iuse)
		}
		fmt.Printf("Active: %s\n", active)

	case "size":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "equery size: missing atom argument\n")
			os.Exit(1)
		}
		size, err := equery.Size(vdbPath, arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery size: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(formatSize(size))

	case "check":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "equery check: missing atom argument\n")
			os.Exit(1)
		}
		mismatches, err := equery.Check(vdbPath, arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery check: %v\n", err)
			os.Exit(1)
		}
		if len(mismatches) == 0 {
			fmt.Println("OK")
		} else {
			for _, m := range mismatches {
				fmt.Println(m)
			}
		}

	case "which":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "equery which: missing atom argument\n")
			os.Exit(1)
		}
		path, err := equery.Which(repoDir, arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "equery which: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(path)

	default:
		fmt.Fprintf(os.Stderr, "equery: unknown subcommand %q\n", subcmd)
		fmt.Fprintf(os.Stderr, "Expected: belongs, files, uses, size, check, which\n")
		os.Exit(1)
	}
}

func writeEquerySubcommandHelp(writer io.Writer, subcommand string) bool {
	usage := map[string]string{
		"belongs": "arise equery belongs <path>",
		"files":   "arise equery files <atom>",
		"uses":    "arise equery uses <atom>",
		"size":    "arise equery size <atom>",
		"check":   "arise equery check <atom>",
		"which":   "arise equery which <atom>",
	}
	line, ok := usage[subcommand]
	if !ok {
		return false
	}
	fmt.Fprintf(writer, "Usage: %s\n", line)
	return true
}

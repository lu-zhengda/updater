package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	root := flag.String("root", ".", "module root to scan")
	baseline := flag.String("baseline", "", "committed per-file counts")
	ratchet := flag.String("ratchet", "", "previous baseline; counts may not increase")
	write := flag.Bool("write", false, "rewrite -baseline from the scan")
	flag.Parse()
	if *baseline == "" {
		_, _ = fmt.Fprintln(os.Stderr, "commentcheck: -baseline is required")
		os.Exit(2)
	}
	if *write {
		total, err := WriteBaseline(*root, *baseline)
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_, _ = fmt.Fprintf(os.Stderr, "commentcheck: wrote %d disallowed lines\n", total)
		return
	}
	total, err := Check(*root, *baseline, *ratchet)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = fmt.Fprintf(os.Stderr, "commentcheck: %d disallowed lines\n", total)
}

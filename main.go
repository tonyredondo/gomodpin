// gomodpin appends a sorted "replace" block to a target go.mod to pin dependencies
// to the versions already in use by that module. It first writes a backup copy of the
// original file to go.mod.old, then appends the generated block to the original go.mod.
//
// Why: Pinning prevents accidental upgrades when running commands like "go get -u" or when
// transitive dependencies shift. By appending a replace block, we make the dependency
// resolution explicit and reproducible without changing the existing require list.
//
// Design choices:
//   - Verbose logs are printed to stdout under -v so they are human-readable but do not
//     interfere with the machine-readable path (we write to go.mod directly rather than stdout).
//   - We respect go.mod's existing "replace" and "exclude" directives, then apply default
//     excludes plus any user-provided excludes via -exclude.
//   - Output is kept stable by iterating modules in a lexicographic order.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// stringSliceFlag implements flag.Value to allow repeating -exclude flags.
// Why: Users often need to exclude more than one module; repeating the flag is ergonomic
// and avoids inventing custom separators.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return fmt.Sprintf("%v", []string(*s)) }
func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	// Flags control logging and exclusion behavior.
	// -v: emit progress logs to stdout; the tool writes data directly to the target go.mod.
	// -no-default-excludes: disable the built-in excludes for dd-trace-go/orchestrion.
	// -exclude: add custom module paths to exclude; can be repeated.
	var verbose bool
	var noDefaultExcludes bool
	var userExcludes stringSliceFlag

	flag.BoolVar(&verbose, "v", false, "enable verbose logs")
	flag.BoolVar(&noDefaultExcludes, "no-default-excludes", false, "disable default excludes (dd-trace-go and orchestrion)")
	flag.Var(&userExcludes, "exclude", "module path to exclude; can be repeated")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [flags] /path/to/go.mod\n", os.Args[0])
		fmt.Fprintln(flag.CommandLine.Output(), "Flags:")
		flag.PrintDefaults()
	}
	flag.Parse()

	// Exactly one positional argument: the path to the target go.mod.
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	modPath := flag.Arg(0)

	// Validate the provided path points to a file named go.mod. Fail early with clear errors
	// to make it easy to fix invocation issues.
	info, err := os.Stat(modPath)
	if err != nil {
		log.Fatalf("error accessing path: %v", err)
	}
	if info.IsDir() {
		log.Fatalf("provided path is a directory; expected path to a go.mod file")
	}
	if filepath.Base(modPath) != "go.mod" {
		log.Fatalf("provided path must be a go.mod file; got %q", filepath.Base(modPath))
	}

	// Read the file contents and parse using x/mod/modfile to inspect requires, replaces, and excludes.
	data, err := os.ReadFile(modPath)
	if err != nil {
		log.Fatalf("error reading go.mod: %v", err)
	}

	f, err := modfile.Parse(modPath, data, nil)
	if err != nil {
		log.Fatalf("error parsing go.mod: %v", err)
	}

	// Build a map of modules to the versions we intend to pin. Start with the require list,
	// then adjust per replace/exclude directives.
	modules := make(map[string]string)
	for _, require := range f.Require {
		modules[require.Mod.Path] = require.Mod.Version
	}

	// Apply replace directives:
	// - If a replace points to a different module path, drop the original from consideration.
	// - If it is a self-replace (same path), override the version to pin to the replaced version.
	for _, replace := range f.Replace {
		if replace.Old.Path != replace.New.Path {
			delete(modules, replace.Old.Path)
		} else {
			if verbose {
				fmt.Printf("Replacing: %s with %s\n", replace.Old.Path, replace.New.Version)
			}
			modules[replace.Old.Path] = replace.New.Version
		}
	}

	// Apply exclude directives from go.mod: remove excluded modules entirely.
	for _, exclude := range f.Exclude {
		if version, exists := modules[exclude.Mod.Path]; exists {
			if verbose {
				fmt.Printf("Excluding (from go.mod exclude) %s@%s\n", exclude.Mod.Path, version)
			}
			delete(modules, exclude.Mod.Path)
		}
	}

	// Combine default excludes and user-provided -exclude flags into a set and remove them.
	// Why: These modules are intentionally managed independently and should not be pinned
	// by this tool unless explicitly desired.
	defaultExcludes := []string{
		"gopkg.in/DataDog/dd-trace-go.v1",
		"github.com/DataDog/dd-trace-go/v2",
		"github.com/DataDog/orchestrion",
	}
	if noDefaultExcludes {
		defaultExcludes = nil
	}

	excludeSet := make(map[string]struct{})
	for _, p := range defaultExcludes {
		excludeSet[p] = struct{}{}
	}
	for _, p := range userExcludes {
		excludeSet[p] = struct{}{}
	}
	for p := range excludeSet {
		if _, ok := modules[p]; ok {
			if verbose {
				fmt.Printf("Excluding (from flags) %s\n", p)
			}
			delete(modules, p)
		}
	}

	// Build the replace block in memory. We skip entries with empty versions to avoid noise.
	// Using a strings.Builder is efficient for concatenation, and we rely on processMapOrdered
	// for stable ordering to keep diffs clean.
	var b strings.Builder
	b.WriteString("\n\n")
	// This comment will be appended to go.mod to mark the intent of the block.
	b.WriteString("// prevent module upgrades\n")
	b.WriteString("replace (\n")
	count := 0
	processMapOrdered(modules, func(path, newVersion string) {
		if newVersion == "" {
			if verbose {
				fmt.Printf("Skipping %s due to empty version\n", path)
			}
			return
		}
		b.WriteString(fmt.Sprintf("\t%s => %s %s\n", path, path, newVersion))
		count++
	})
	b.WriteString(")\n")

	// Safety: write a full backup before mutating go.mod.
	backupPath := filepath.Join(filepath.Dir(modPath), "go.mod.old")
	if err := os.WriteFile(backupPath, data, info.Mode().Perm()); err != nil {
		log.Fatalf("error writing backup file: %v", err)
	}
	if verbose {
		fmt.Printf("Backed up %s to %s\n", modPath, backupPath)
	}

	// If there is nothing to append, exit early after producing the backup.
	if count == 0 {
		if verbose {
			fmt.Printf("No replacements to append\n")
		}
		return
	}

	// Append the block to the existing go.mod, preserving permissions and avoiding truncation.
	fh, err := os.OpenFile(modPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		log.Fatalf("error opening go.mod for appending: %v", err)
	}
	defer fh.Close()
	if _, err := fh.WriteString(b.String()); err != nil {
		log.Fatalf("error appending replace block: %v", err)
	}
	if verbose {
		fmt.Printf("Appended %d replacements to %s\n", count, modPath)
	}
}

// processMapOrdered calls f for each key/value in m with keys sorted lexicographically.
// Why: Deterministic ordering makes generated output stable, which simplifies reviews
// and future diffs.
func processMapOrdered(m map[string]string, f func(key, value string)) {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	for _, key := range keys {
		f(key, m[key])
	}
}

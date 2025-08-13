## gomodpin

Pin every dependency currently used by a Go module by appending a stable, sorted `replace` block to its `go.mod`.

This makes dependency resolution explicit and reproducible: accidental upgrades from `go get -u`, indirect dependency shifts, or subtle resolver changes will no longer move versions without you seeing them in a diff.

### Why gomodpin?

- **Reproducibility**: Locks the module graph to the versions already in use.
- **Safety-first**: Writes a backup `go.mod.old` before making any change.
- **Low noise**: Keeps your existing `require` list intact; only appends a `replace` block.
- **Deterministic**: Outputs replacements in lexicographic order for clean, reviewable diffs.
- **Respectful**: Honors existing `replace` and `exclude` directives in `go.mod`.
- **Configurable**: Exclude modules you don’t want pinned, including a default set you can disable.

## Installation

Prerequisites: Go 1.23+ (the module `go` directive is `1.23.2`).

From the project root:

```bash
# Install into $GOBIN (or $GOPATH/bin) so `gomodpin` is on your PATH
go install .

# Or build a local binary in the repo root
go build -o gomodpin .
```

Verify installation:

```bash
gomodpin -h
```

## Usage

Basic form:

```bash
gomodpin [flags] /path/to/go.mod
```

Flags:

- `-v`: **enable verbose logs**.
- `-no-default-excludes`: **disable default excludes** so even those modules are pinned.
- `-exclude <module>`: **exclude module path from pinning** (repeatable).

Default excludes (unless `-no-default-excludes` is set):

- `gopkg.in/DataDog/dd-trace-go.v1`
- `github.com/DataDog/dd-trace-go/v2`
- `github.com/DataDog/orchestrion`

### Examples

- **Pin the current module** (run from a repository root that contains a `go.mod`):

```bash
gomodpin ./go.mod
```

- **Verbose output** to see what’s happening:

```bash
gomodpin -v ./go.mod
```

- **Exclude specific modules** in addition to the defaults:

```bash
gomodpin -exclude github.com/myorg/internal-tool -exclude golang.org/x/sys ./go.mod
```

- **Include the default-excluded modules** (i.e., pin everything):

```bash
gomodpin -no-default-excludes ./go.mod
```

- **Use in a monorepo** with many `go.mod` files:

```bash
git ls-files | grep '/go\.mod$' | while read -r mod; do
  gomodpin "$mod"
done
```

## What it does (and doesn’t)

- **Reads** your `go.mod` and builds a map of modules from the `require` list.
- **Respects existing `replace` directives**:
  - If `replace` points to a different module path, the original module is removed from consideration.
  - If it replaces the same path, that replacement version is used for pinning.
- **Respects `exclude` directives** from your `go.mod`.
- **Applies excludes** from flags and the default set (unless disabled).
- **Appends** a comment and a single `replace (...)` block with entries like:

```go
// prevent module upgrades
replace (
    <module> => <module> <version>
    # repeated for each module in lexicographic order
)
```

- **Does not** modify the existing `require` section.
- **Does not** attempt to be idempotent: running multiple times will append multiple `replace` blocks. Prefer to run once per change or clean up previous blocks if needed.

## Before and after

Assume this `go.mod` snippet before running:

```go
module example.com/my/service

require (
    github.com/pkg/errors v0.9.1
    golang.org/x/sys v0.16.0 // indirect
)
```

After running `gomodpin ./go.mod` you’ll see an appended block:

```go
// prevent module upgrades
replace (
    github.com/pkg/errors => github.com/pkg/errors v0.9.1
    golang.org/x/sys => golang.org/x/sys v0.16.0
)
```

Your original file is preserved as `go.mod.old` in the same directory.

## Design notes

- **Backup first**: `go.mod.old` is written with the same permissions as the original.
- **Stable ordering**: modules are sorted lexicographically to produce clean diffs.
- **Skip empty versions**: any module with an empty version is omitted from the `replace` block.
- **Logging**: `-v` prints progress like replacements and exclusions.

## Exit behavior

- Non-zero exit with a clear error message if:
  - The provided path doesn’t exist or isn’t a file.
  - The provided file isn’t named `go.mod`.
  - The `go.mod` cannot be parsed.
- Zero exit after writing the backup even if there were **no replacements** to append.

## Reverting changes

- If you haven’t committed yet, simply restore the backup:

```bash
mv go.mod.old go.mod
```

- If you have committed, use your VCS to revert.

Note: By default, `.gitignore` includes `go.mod.old`.

## Recommended workflows

- **One-time pin** before a release or after dependency churn:

```bash
gomodpin -v ./go.mod && git add go.mod && git commit -m "Pin dependencies with gomodpin"
```

- **CI guardrail** to detect unexpected changes:
  - Run `gomodpin` and then `git diff --exit-code go.mod` to fail the build if pinning would change the file.

## Troubleshooting

- **provided path is a directory; expected path to a go.mod file**
  - Pass the path to the file itself (e.g., `./go.mod`), not the directory.

- **provided path must be a go.mod file**
  - The tool only operates on files named exactly `go.mod`.

- **No replacements to append**
  - The current `go.mod` may already have effective pins via replaces or no modules in `require`.

- **Repeated runs keep appending blocks**
  - This is expected; the tool is append-only. Clean up older blocks manually if needed, or revert with the backup.

## Limitations

- Append-only; does not deduplicate prior `replace` blocks.
- Operates on the modules currently listed in `require` (including indirects). If a module isn’t required, it won’t be pinned.
- Default excludes are project-specific; disable with `-no-default-excludes` to pin everything.

## Contributing

- Open issues and PRs are welcome. Please run `go build` before submitting.

## Acknowledgments

- Built on top of `golang.org/x/mod/modfile` to parse and examine `go.mod` files.



package canticles

import (
	"encoding/json"
	"errors"
	"go/build"
	"os"
	"os/exec"
	"strings"
)

type PackageError struct {
	ImportStack []string // shortest path from package named on command line to this one
	Pos         string   // position of error
	Err         string   // the error itself
}

func (pe PackageError) Error() string {
	return pe.Err
}

// IsNoBuildable returns true if this error was caused by a lack of
// buildable source files.
func (pe PackageError) IsNoBuildable() bool {
	return strings.HasPrefix(pe.Err, "no buildable Go")
}

// A Package describes a go single package found in a directory.  This
// is from the go source code cmd/go. As it is a main package we can
// not import it. We use this to interpret the output of `go list
// --json.`
type Package struct {
	// Note: These fields are part of the go command's public API.
	// See list.go.  It is okay to add fields, but not to change or
	// remove existing ones.  Keep in sync with list.go
	Dir         string `json:",omitempty"` // directory containing package sources
	ImportPath  string `json:",omitempty"` // import path of package in dir
	Name        string `json:",omitempty"` // package name
	Doc         string `json:",omitempty"` // package documentation string
	Target      string `json:",omitempty"` // install path
	Goroot      bool   `json:",omitempty"` // is this package found in the Go root?
	Standard    bool   `json:",omitempty"` // is this package part of the standard Go library?
	Stale       bool   `json:",omitempty"` // would 'go install' do anything for this package?
	Root        string `json:",omitempty"` // Go root or Go path dir containing this package
	ConflictDir string `json:",omitempty"` // Dir is hidden by this other directory

	// Source files
	GoFiles        []string `json:",omitempty"` // .go source files (excluding CgoFiles, TestGoFiles, XTestGoFiles)
	CgoFiles       []string `json:",omitempty"` // .go sources files that import "C"
	IgnoredGoFiles []string `json:",omitempty"` // .go sources ignored due to build constraints
	CFiles         []string `json:",omitempty"` // .c source files
	CXXFiles       []string `json:",omitempty"` // .cc, .cpp and .cxx source files
	HFiles         []string `json:",omitempty"` // .h, .hh, .hpp and .hxx source files
	SFiles         []string `json:",omitempty"` // .s source files
	SwigFiles      []string `json:",omitempty"` // .swig files
	SwigCXXFiles   []string `json:",omitempty"` // .swigcxx files
	SysoFiles      []string `json:",omitempty"` // .syso system object files added to package

	// Cgo directives
	CgoCFLAGS    []string `json:",omitempty"` // cgo: flags for C compiler
	CgoCPPFLAGS  []string `json:",omitempty"` // cgo: flags for C preprocessor
	CgoCXXFLAGS  []string `json:",omitempty"` // cgo: flags for C++ compiler
	CgoLDFLAGS   []string `json:",omitempty"` // cgo: flags for linker
	CgoPkgConfig []string `json:",omitempty"` // cgo: pkg-config names

	// Dependency information
	Imports []string `json:",omitempty"` // import paths used by this package
	Deps    []string `json:",omitempty"` // all (recursively) imported dependencies

	// Error information
	Incomplete bool            `json:",omitempty"` // was there an error loading this package or dependencies?
	Error      *PackageError   `json:",omitempty"` // error loading this package (not dependencies)
	DepsErrors []*PackageError `json:",omitempty"` // errors loading dependencies

	// Test information
	TestGoFiles  []string `json:",omitempty"` // _test.go files in package
	TestImports  []string `json:",omitempty"` // imports from TestGoFiles
	XTestGoFiles []string `json:",omitempty"` // _test.go files outside package
	XTestImports []string `json:",omitempty"` // imports from XTestGoFiles
}

// IsRemote returns true if the importPath is a remote
// importpath. That is it has a domain name and has at least one path
// part.
func IsRemote(importPath string) bool {
	if build.IsLocalImport(importPath) {
		return false
	}

	// If our first token ends in a domain
	// name we will treat this as a network path
	// (Standard library imports won't have this)
	parts := strings.Split(importPath, "/")
	if len(parts) < 2 {
		return false
	}

	domainParts := strings.Split(parts[0], ".")
	if len(domainParts) < 2 {
		return false
	}

	return true
}

func filterStrings(strings []string, f func(string) bool) []string {
	filtered := make([]string, 0, len(strings))

	for _, s := range strings {
		if f(s) {
			filtered = append(filtered, s)
		}
	}

	return filtered
}

// LoadPackage uses `go list --json` to get details about a local go
// package. Path should be the import path of the package. Package
// will be nil if an error occurs. Package itself may also have
// errors.
func LoadPackage(pkgPath, gohome string) (*Package, error) {
	cmd := exec.Command("go", "list", "--json", "-e", pkgPath)
	LogVerbose("Running command go list --json -e %s", pkgPath)
	cmd.Env = PatchEnviroment(os.Environ(), "GOPATH", gohome)
	result, err := cmd.CombinedOutput()
	if err != nil {
		return nil, errors.New(string(result))
	}

	pkg := &Package{}
	if err := json.Unmarshal(result, pkg); err != nil {
		return nil, err
	}

	if pkg.Error != nil {
		return nil, pkg.Error
	}

	return pkg, nil
}

// RemoteImports returns the packages set of remote imports (as
// defined by IsRemote).
func (p *Package) RemoteImports(includeTest bool) []string {
	imports := p.Imports
	if includeTest {
		imports = append(imports, p.TestImports...)
	}

	return filterStrings(imports, IsRemote)
}

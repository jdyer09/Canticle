package canticles

import (
	"errors"
	"fmt"
	"os"
	"sort"
)

// PkgReaderFunc takes a given package string and returns all
// the dependencies for that package. If error is not nil on
// return the walker halts and returns the error.
type PkgReaderFunc func(pkg string) ([]string, error)

// PkgHandlerFunc is called once for each loaded package. If the error
// ErrorSkip is returned deps or this package are no read. All other
// non nil errors halt the walker and return the value.
type PkgHandlerFunc func(pkg string) error

// ErrorSkip tells a walker to skip loading the deps of this dep.
var ErrorSkip = errors.New("skip this dep")

// DependencyWalker is used to walker the dependencies of a package.
// It will walk the dependencies for an import path only once.
type DependencyWalker struct {
	nodeQueue   []string
	visited     map[string]bool
	readPackage PkgReaderFunc
	handleDep   PkgHandlerFunc
}

// NewDependencyWalker creates a new dep loader. It uses the
// specified  depReader to load dependencies. It will call the handler
// with the resulting dependencies.
func NewDependencyWalker(reader PkgReaderFunc, handler PkgHandlerFunc) *DependencyWalker {
	return &DependencyWalker{
		visited:     make(map[string]bool),
		handleDep:   handler,
		readPackage: reader,
	}
}

// TraverseDependencies reads and loads all dependencies of dep. It is
// a breadth first search. If handler returns the special error
// ErrorSkip it does not read the deps of this package.
func (dw *DependencyWalker) TraverseDependencies(pkg string) error {
	dw.nodeQueue = append(dw.nodeQueue, pkg)
	for len(dw.nodeQueue) > 0 {
		// Dequeue and mark loaded
		p := dw.nodeQueue[0]
		dw.nodeQueue = dw.nodeQueue[1:]
		dw.visited[p] = true
		LogVerbose("Handling pkg: %+v", p)

		// Inform our handler of this package
		err := dw.handleDep(p)
		switch {
		case err == ErrorSkip:
			continue
		case err != nil:
			return err
		}

		// Read out our children
		children, err := dw.readPackage(p)
		if err != nil {
			return fmt.Errorf("cant read deps of package %s with error %s", pkg, err.Error())
		}
		sort.Strings(children)
		LogVerbose("Package %s has children %v", p, children)

		for _, child := range children {
			if dw.visited[child] {
				continue
			}
			dw.nodeQueue = append(dw.nodeQueue, child)
		}
	}

	return nil
}

// A DependencyReader reads the set of deps for a package
type DependencyReader func(importPath string) (Dependencies, error)

// A DependencyLoader fetches and set the correct revision for a
// dependency using the specified resolver.
type DependencyLoader struct {
	deps     Dependencies
	cdeps    []*CanticleDependency
	gopath   string
	resolver RepoResolver
	readDeps DependencyReader
}

// NewDependencyLoader returns a DependencyLoader initialized with the
// resolver func.
func NewDependencyLoader(resolver RepoResolver, depReader DependencyReader, cdeps []*CanticleDependency, gopath string) *DependencyLoader {
	return &DependencyLoader{
		deps:     NewDependencies(),
		readDeps: depReader,
		resolver: resolver,
		cdeps:    cdeps,
		gopath:   gopath,
	}
}

// TODO: This shares a ton of code with depsaver, look into that

// FetchUpdatePackage will fetch or set the specified path to the version
// defined by the Dependency or if no version is defined will use
// the VCS default.
func (dl *DependencyLoader) FetchUpdatePackage(pkg string) error {
	LogVerbose("DepLoader handling pkg: %s", pkg)
	path := PackageSource(dl.gopath, pkg)

	// See if this path is on disk, if so we don't need to fetch anything
	ondisk := true
	s, err := os.Stat(path)
	switch {
	case err != nil && os.IsNotExist(err):
		ondisk = false
	case err != nil:
		fmt.Errorf("cant fetch package error when stating import path %s", err.Error())
	case s != nil && !s.IsDir():
		return fmt.Errorf("cant fetch pkg for path %s is a file not a directory", path)
	}

	// Fetch the package
	LogVerbose("DepLoader check path: %s", path)
	if !ondisk {
		// Resolve the vcs using our cdep if available
		cdep := dl.cdepForPkg(pkg)
		LogVerbose("Resolving repo for %s ondisk %v path %s", pkg, ondisk, path)
		vcs, err := dl.resolver.ResolveRepo(pkg, cdep)
		if err != nil {
			return fmt.Errorf("%s version control %s", pkg, err.Error())
		}

		if err := dl.fetchPackage(vcs, cdep); err != nil {
			return fmt.Errorf("cant fetch package %s %s", pkg, err.Error())
		}
	}

	// Load all the deps for this file directly
	LogVerbose("DepLoader reading deps of path: %s", path)
	deps, err := dl.readDeps(path)
	if err != nil {
		return fmt.Errorf("package %s couldn't read deps %s", pkg, err.Error())
	}
	LogVerbose("Read package %s deps:\n[\n%+v]", pkg, deps)

	// Setup our deps
	dep := NewDependency(pkg)
	for _, d := range deps {
		d.ImportedFrom.Add(pkg)
	}
	dl.deps.AddDependencies(deps)
	for _, pkgDep := range deps {
		dep.Imports.Add(pkgDep.ImportPath)
	}
	LogVerbose("Adding dep %+v\n", dep)
	dl.deps.AddDependency(dep)

	return nil
}

func (dl *DependencyLoader) cdepForPkg(pkg string) *CanticleDependency {
	for _, dep := range dl.cdeps {
		if PathIsChild(dep.Root, pkg) {
			return dep
		}
	}
	return nil
}

// PackagePaths determines the set of import paths for package.
func (dl *DependencyLoader) PackageImports(pkg string) ([]string, error) {
	dep := dl.deps.Dependency(pkg)
	if dep == nil {
		return []string{}, fmt.Errorf("no dep for %s, should not be requested", pkg)
	}
	return dep.Imports.Array(), nil
}

func (dl *DependencyLoader) setRevision(vcs VCS, dep *CanticleDependency) error {
	LogVerbose("Setting rev on dep %+v", dep)
	if err := vcs.SetRev(""); err != nil {
		return fmt.Errorf("failed to set revision because %s", err.Error())
	}
	return nil
}

func (dl *DependencyLoader) fetchPackage(vcs VCS, dep *CanticleDependency) error {
	LogVerbose("Fetching dep %+v", dep)
	if err := vcs.Create(""); err != nil {
		return fmt.Errorf("failed to fetch because %s", err.Error())
	}
	return nil
}

type DepReaderFunc func(importPath string) (Dependencies, error)

// DependencySaver is a handler for dependencies that will save all
// dependencies current revisions. Call Dependencies() to retrieve the
// loaded Dependencies.
type DependencySaver struct {
	deps   Dependencies
	gopath string
	root   string
	read   DepReaderFunc
	// NoRecur contains a list of directories this will not recur
	// into under root.
	NoRecur StringSet
}

// NewDependencySaver builds a new dependencysaver to work in the
// specified gopath and resolve using the resolverfunc. A
// DependencySaver should generally only be used once. A
// DependencySaver will not attempt to load remote dependencies even
// if the resolverfunc can handle them. Deps that resolve using ignore
// will not be saved.
func NewDependencySaver(reader DepReaderFunc, gopath, root string) *DependencySaver {
	return &DependencySaver{
		deps:    NewDependencies(),
		root:    root,
		read:    reader,
		gopath:  gopath,
		NoRecur: NewStringSet(),
	}
}

// SavePackageDeps uses the reader to read all 1st order deps of this
// pkg.
func (ds *DependencySaver) SavePackageDeps(path string) error {
	LogVerbose("Examine path %s", path)
	pkg, err := PackageName(ds.gopath, path)
	if err != nil {
		return fmt.Errorf("Error getting package name for path %s", path)
	}

	// Check if we can find this package
	s, err := os.Stat(path)
	switch {
	case s != nil && !s.IsDir():
		err = fmt.Errorf("cant save deps for path %s is a file not a directory", path)
	case err != nil && os.IsNotExist(err):
		err = fmt.Errorf("cant save deps for path %s could not be found on disk", path)
	case err != nil:
		err = fmt.Errorf("cant save deps for path %s due to %s", path, err.Error())
	}
	if err != nil {
		LogVerbose("Error stating path %s %s", path, err.Error())
		dep := NewDependency(pkg)
		dep.Err = err
		ds.deps.AddDependency(dep)
		return ErrorSkip
	}
	// Don't attempt to read the dependencies of the "src" dir...
	if path == PackageSource(ds.gopath, "") {
		return nil
	}

	// If we get back a no buildable with no read imports return
	// nil (this is an empty dir, so we don't want it in our
	// package setup). If we have any pkgDeps though (from a cant file)
	// we need this.
	pkgDeps, err := ds.read(path)
	if len(pkgDeps) == 0 && err != nil {
		if e, ok := err.(*PackageError); ok {
			if e.IsNoBuildable() {
				LogVerbose("Unbuildable pkg")
				return nil
			}
		}
		LogVerbose("Error reading pkg deps %s %s", pkg, err.Error())
		dep := NewDependency(pkg)
		dep.Err = fmt.Errorf("cant read deps for package %s %s", pkg, err.Error())
		ds.deps.AddDependency(dep)
		return nil
	}

	dep := NewDependency(pkg)
	for _, d := range pkgDeps {
		d.ImportedFrom.Add(pkg)
	}
	ds.deps.AddDependencies(pkgDeps)
	for _, pkgDep := range pkgDeps {
		dep.Imports.Add(pkgDep.ImportPath)
	}
	LogVerbose("Adding dep for pkg %v", dep)
	ds.deps.AddDependency(dep)
	return nil
}

// PackagePaths returns d all import paths for a pkg, and all subdirs
// if the pkg is under the root of the passed to the ds at construction.
func (ds *DependencySaver) PackagePaths(path string) ([]string, error) {
	paths := NewStringSet()
	if PathIsChild(ds.root, path) {
		subdirs, err := VisibleSubDirectories(path)
		if err != nil {
			return []string{}, err
		}
		paths.Add(subdirs...)
		LogVerbose("Package has subdirs %v", subdirs)
	}
	paths.Difference(ds.NoRecur)
	pkg, err := PackageName(ds.gopath, path)
	if err != nil {
		LogVerbose("Package name error %s", err.Error())
		return []string{}, err
	}
	dep := ds.deps.Dependency(pkg)
	if dep == nil {
		LogVerbose("Package has no dep %s", pkg)
		return paths.Array(), nil
	}
	if dep.Err != nil {
		LogVerbose("Package dep err not nil %s %v", pkg, dep.Err)
		return []string{}, nil
	}
	imports := dep.Imports.Array()
	for _, imp := range imports {
		paths.Add(PackageSource(ds.gopath, imp))
	}
	LogVerbose("Package has imports %v", imports)
	return paths.Array(), nil
}

// Dependencies returns the resolved dependencies from dependency
// saver.
func (ds *DependencySaver) Dependencies() Dependencies {
	return ds.deps
}

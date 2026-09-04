package modproxy

import (
	"archive/zip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"go/doc"
	"go/parser"
	"go/token"
	"hash/fnv"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	modzip "golang.org/x/mod/zip"
	"golang.org/x/pkgsite/cmd/pkgsiteembed"
)

type Store struct {
	modules  map[string]*storedModule
	packages []Package
}

type storedModule struct {
	path     string
	versions map[string]*storedVersion
	ordered  []string
	latest   string
}

type storedVersion struct {
	version  string
	info     string
	mod      string
	zip      string
	modTime  time.Time
	infoTime time.Time
}

type versionInfo struct {
	Version string
	Time    time.Time
}

type Package struct {
	Name          string
	Path          string
	ModulePath    string
	Version       string
	Synopsis      string
	CommitTime    time.Time
	Licenses      []string
	ImportedBy    []string
	NumImportedBy uint64
	imports       []string
}

func Open(roots []string) (*Store, error) {
	store := &Store{modules: make(map[string]*storedModule)}
	for _, root := range roots {
		if err := store.index(root); err != nil {
			return nil, err
		}
	}
	for _, mod := range store.modules {
		for version := range mod.versions {
			mod.ordered = append(mod.ordered, version)
		}
		sort.Slice(mod.ordered, func(i, j int) bool {
			return semver.Compare(mod.ordered[i], mod.ordered[j]) > 0
		})
		mod.latest = latestVersion(mod)
		latest := mod.versions[mod.latest]
		packages, err := packagesFromZip(mod.path, mod.latest, latest.zip, latest.infoTime)
		if err != nil {
			return nil, err
		}
		store.packages = append(store.packages, packages...)
	}
	populateImportedBy(store.packages)
	sort.Slice(store.packages, func(i, j int) bool {
		return store.packages[i].Path < store.packages[j].Path
	})
	return store, nil
}

// Fingerprint returns a stable digest of the proxy artifacts that affect the
// index. It reads directory metadata only; archive contents are not loaded.
func Fingerprint(roots []string) (uint64, error) {
	hash := fnv.New64a()
	var number [8]byte
	for index, root := range roots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return 0, fmt.Errorf("resolve proxy directory %q: %w", root, err)
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return 0, fmt.Errorf("inspect proxy directory %q: %w", root, err)
		}
		if !info.IsDir() {
			return 0, fmt.Errorf("proxy path %q is not a directory", root)
		}
		binary.LittleEndian.PutUint64(number[:], uint64(index))
		_, _ = hash.Write(number[:])
		err = filepath.WalkDir(absolute, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !isProxyArtifact(entry.Name()) {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(absolute, current)
			if err != nil {
				return err
			}
			_, _ = hash.Write([]byte(filepath.ToSlash(relative)))
			_, _ = hash.Write([]byte{0})
			binary.LittleEndian.PutUint64(number[:], uint64(info.Size()))
			_, _ = hash.Write(number[:])
			binary.LittleEndian.PutUint64(number[:], uint64(info.ModTime().UnixNano()))
			_, _ = hash.Write(number[:])
			return nil
		})
		if err != nil {
			return 0, fmt.Errorf("fingerprint proxy directory %q: %w", root, err)
		}
	}
	return hash.Sum64(), nil
}

func isProxyArtifact(name string) bool {
	return name == "source.zip" || name == "go.mod" || name == "list" ||
		strings.HasSuffix(name, ".zip") ||
		strings.HasSuffix(name, ".mod") ||
		strings.HasSuffix(name, ".info")
}

var ErrVersionExists = errors.New("module version already exists")

// AddVersion writes a complete version to a standard GOPROXY directory. The
// zip is published last, so concurrent scans never observe a partial version.
func AddVersion(root, modulePath, version string, infoReader, modReader, zipReader io.Reader) error {
	if err := module.Check(modulePath, version); err != nil {
		return fmt.Errorf("invalid module version %s@%s: %w", modulePath, version, err)
	}
	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		return fmt.Errorf("escape module path: %w", err)
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		return fmt.Errorf("escape module version: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve proxy directory %q: %w", root, err)
	}
	if info, err := os.Stat(root); err != nil {
		return fmt.Errorf("inspect proxy directory %q: %w", root, err)
	} else if !info.IsDir() {
		return fmt.Errorf("proxy path %q is not a directory", root)
	}

	infoData, err := readLimited(infoReader, 1<<20)
	if err != nil {
		return fmt.Errorf("read info file: %w", err)
	}
	var versionInfo versionInfo
	if err := json.Unmarshal(infoData, &versionInfo); err != nil {
		return fmt.Errorf("parse info file: %w", err)
	}
	if versionInfo.Version != version || versionInfo.Time.IsZero() {
		return fmt.Errorf("info file must contain version %q and a non-zero time", version)
	}
	modData, err := readLimited(modReader, 16<<20)
	if err != nil {
		return fmt.Errorf("read mod file: %w", err)
	}
	if declared := modfile.ModulePath(modData); declared != modulePath {
		return fmt.Errorf("mod file declares %q, want %q", declared, modulePath)
	}

	versionDir := filepath.Join(root, filepath.FromSlash(escapedPath), "@v")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return fmt.Errorf("create module directory: %w", err)
	}
	base := filepath.Join(versionDir, escapedVersion)
	targets := []string{base + ".info", base + ".mod", base + ".zip"}
	for _, target := range targets {
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("%w: %s@%s", ErrVersionExists, modulePath, version)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect target %q: %w", target, err)
		}
	}

	tempZip, err := os.CreateTemp(versionDir, ".gopkg-upload-*.zip")
	if err != nil {
		return fmt.Errorf("create temporary zip: %w", err)
	}
	tempZipName := tempZip.Name()
	defer os.Remove(tempZipName)
	written, copyErr := io.Copy(tempZip, io.LimitReader(zipReader, (500<<20)+1))
	closeErr := tempZip.Close()
	if copyErr != nil {
		return fmt.Errorf("write temporary zip: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close temporary zip: %w", closeErr)
	}
	if written > 500<<20 {
		return errors.New("zip file exceeds 500 MiB")
	}
	if _, err := modzip.CheckZip(module.Version{Path: modulePath, Version: version}, tempZipName); err != nil {
		return fmt.Errorf("validate module zip: %w", err)
	}

	tempInfo, err := writeTemp(versionDir, ".gopkg-upload-*.info", infoData)
	if err != nil {
		return err
	}
	defer os.Remove(tempInfo)
	tempMod, err := writeTemp(versionDir, ".gopkg-upload-*.mod", modData)
	if err != nil {
		return err
	}
	defer os.Remove(tempMod)
	published := false
	defer func() {
		if published {
			return
		}
		for _, target := range targets {
			_ = os.Remove(target)
		}
	}()
	if err := os.Rename(tempInfo, targets[0]); err != nil {
		return fmt.Errorf("publish info file: %w", err)
	}
	if err := os.Rename(tempMod, targets[1]); err != nil {
		return fmt.Errorf("publish mod file: %w", err)
	}
	if err := os.Rename(tempZipName, targets[2]); err != nil {
		return fmt.Errorf("publish zip file: %w", err)
	}
	if err := updateVersionList(versionDir, version); err != nil {
		return err
	}
	published = true
	return nil
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}

func writeTemp(dir, pattern string, data []byte) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("create temporary file: %w", err)
	}
	name := file.Name()
	if err := file.Chmod(0o644); err != nil {
		file.Close()
		os.Remove(name)
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func updateVersionList(dir, version string) error {
	listPath := filepath.Join(dir, "list")
	data, err := os.ReadFile(listPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read version list: %w", err)
	}
	versions := strings.Fields(string(data))
	seen := false
	for _, existing := range versions {
		if existing == version {
			seen = true
			break
		}
	}
	if !seen {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool {
		return semver.Compare(versions[i], versions[j]) < 0
	})
	temp, err := writeTemp(dir, ".gopkg-list-*", []byte(strings.Join(versions, "\n")+"\n"))
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	if err := os.Rename(temp, listPath); err != nil {
		return fmt.Errorf("publish version list: %w", err)
	}
	return nil
}

func DiscoverLocal(roots []string) ([]string, error) {
	seen := make(map[string]bool)
	var modules []string
	for _, root := range roots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("resolve local directory %q: %w", root, err)
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return nil, fmt.Errorf("inspect local directory %q: %w", root, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("local path %q is not a directory", root)
		}
		err = filepath.WalkDir(absolute, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() && current != absolute && ignoredDir(entry.Name()) {
				return filepath.SkipDir
			}
			if !entry.IsDir() && entry.Name() == "go.mod" {
				dir := filepath.Dir(current)
				if !seen[dir] {
					seen[dir] = true
					modules = append(modules, dir)
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan local directory %q: %w", root, err)
		}
	}
	sort.Strings(modules)
	return modules, nil
}

func (s *Store) Modules() []string {
	modules := make([]string, 0, len(s.modules))
	for _, mod := range s.modules {
		modules = append(modules, mod.path)
	}
	sort.Strings(modules)
	return modules
}

func (s *Store) Packages() []Package {
	return append([]Package(nil), s.packages...)
}
func (s *Store) HasVersion(modulePath, version string) bool {
	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		return false
	}
	stored := s.modules[escapedPath]
	return stored != nil && stored.versions[version] != nil
}

func (s *Store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	requestPath := strings.TrimPrefix(r.URL.Path, "/")
	if strings.HasSuffix(requestPath, "/@latest") {
		moduleKey := strings.TrimSuffix(requestPath, "/@latest")
		mod := s.modules[moduleKey]
		if mod == nil || len(mod.ordered) == 0 {
			http.NotFound(w, r)
			return
		}
		s.serveInfo(w, r, mod.versions[mod.latest])
		return
	}

	const marker = "/@v/"
	index := strings.LastIndex(requestPath, marker)
	if index < 1 {
		http.NotFound(w, r)
		return
	}
	mod := s.modules[requestPath[:index]]
	if mod == nil {
		http.NotFound(w, r)
		return
	}
	file := requestPath[index+len(marker):]
	if file == "list" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for i := len(mod.ordered) - 1; i >= 0; i-- {
			version := mod.ordered[i]
			if !module.IsPseudoVersion(version) {
				_, _ = fmt.Fprintln(w, version)
			}
		}
		return
	}

	dot := strings.LastIndexByte(file, '.')
	if dot < 1 {
		http.NotFound(w, r)
		return
	}
	version, err := module.UnescapeVersion(file[:dot])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	stored := mod.versions[version]
	if stored == nil {
		http.NotFound(w, r)
		return
	}
	switch file[dot+1:] {
	case "info":
		s.serveInfo(w, r, stored)
	case "mod":
		serveFile(w, r, "text/plain; charset=utf-8", stored.mod)
	case "zip":
		serveFile(w, r, "application/zip", stored.zip)
	default:
		http.NotFound(w, r)
	}
}

func (s *Store) index(root string) error {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve proxy directory %q: %w", root, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return fmt.Errorf("inspect proxy directory %q: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("proxy path %q is not a directory", root)
	}

	err = filepath.WalkDir(absolute, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() == "source.zip" {
			return s.indexAthens(absolute, current, entry)
		}
		if strings.HasSuffix(entry.Name(), ".zip") {
			return s.indexStandard(absolute, current, entry)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan proxy directory %q: %w", root, err)
	}
	return nil
}

func (s *Store) indexAthens(root, zipPath string, entry fs.DirEntry) error {
	versionDir := filepath.Dir(zipPath)
	version, err := module.UnescapeVersion(filepath.Base(versionDir))
	if err != nil {
		return fmt.Errorf("decode Athens version for %s: %w", zipPath, err)
	}
	modPath := filepath.Join(versionDir, "go.mod")
	goMod, readErr := os.ReadFile(modPath)
	modulePath := ""
	if readErr == nil {
		modulePath = modfile.ModulePath(goMod)
	}
	if modulePath == "" {
		moduleDir := filepath.Dir(versionDir)
		relative, err := filepath.Rel(root, moduleDir)
		if err != nil {
			return err
		}
		modulePath, err = module.UnescapePath(filepath.ToSlash(relative))
		if err != nil {
			return fmt.Errorf("identify Athens module for %s: %w", zipPath, err)
		}
	}
	if err := module.Check(modulePath, version); err != nil {
		return fmt.Errorf("invalid Athens module %s@%s: %w", modulePath, version, err)
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	s.add(modulePath, &storedVersion{
		version:  version,
		info:     filepath.Join(versionDir, version+".info"),
		mod:      modPath,
		zip:      zipPath,
		modTime:  info.ModTime(),
		infoTime: readInfoTime(filepath.Join(versionDir, version+".info"), info.ModTime()),
	})
	return nil
}

func (s *Store) indexStandard(root, zipPath string, entry fs.DirEntry) error {
	relative, err := filepath.Rel(root, zipPath)
	if err != nil {
		return err
	}
	relative = filepath.ToSlash(relative)
	const marker = "/@v/"
	index := strings.LastIndex(relative, marker)
	if index < 1 {
		return nil
	}
	modulePath, err := module.UnescapePath(relative[:index])
	if err != nil {
		return fmt.Errorf("decode module path for %s: %w", zipPath, err)
	}
	encodedVersion := strings.TrimSuffix(relative[index+len(marker):], ".zip")
	version, err := module.UnescapeVersion(encodedVersion)
	if err != nil {
		return fmt.Errorf("decode module version for %s: %w", zipPath, err)
	}
	if err := module.Check(modulePath, version); err != nil {
		return fmt.Errorf("invalid proxy module %s@%s: %w", modulePath, version, err)
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	base := strings.TrimSuffix(zipPath, ".zip")
	s.add(modulePath, &storedVersion{
		version:  version,
		info:     base + ".info",
		mod:      base + ".mod",
		zip:      zipPath,
		modTime:  info.ModTime(),
		infoTime: readInfoTime(base+".info", info.ModTime()),
	})
	return nil
}

func (s *Store) add(modulePath string, version *storedVersion) {
	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		return
	}
	mod := s.modules[escapedPath]
	if mod == nil {
		mod = &storedModule{path: modulePath, versions: make(map[string]*storedVersion)}
		s.modules[escapedPath] = mod
	}
	if _, exists := mod.versions[version.version]; !exists {
		mod.versions[version.version] = version
	}
}

func latestVersion(mod *storedModule) string {
	var release, prerelease, pseudo string
	var pseudoTime time.Time
	for _, version := range mod.ordered {
		switch {
		case module.IsPseudoVersion(version):
			if candidate := mod.versions[version]; pseudo == "" || candidate.infoTime.After(pseudoTime) {
				pseudo = version
				pseudoTime = candidate.infoTime
			}
		case semver.Prerelease(version) == "":
			if release == "" {
				release = version
			}
		case prerelease == "":
			prerelease = version
		}
	}
	if release != "" {
		return release
	}
	if prerelease != "" {
		return prerelease
	}
	return pseudo
}

func readInfoTime(name string, fallback time.Time) time.Time {
	data, err := os.ReadFile(name)
	if err != nil {
		return fallback
	}
	var info versionInfo
	if json.Unmarshal(data, &info) != nil || info.Time.IsZero() {
		return fallback
	}
	return info.Time
}

func packagesFromZip(modulePath, version, zipPath string, commitTime time.Time) ([]Package, error) {
	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("index packages in %s: %w", zipPath, err)
	}
	defer archive.Close()

	prefix := modulePath + "@" + version
	type packageData struct {
		path     string
		dir      string
		name     string
		synopsis string
		imports  map[string]bool
	}
	indexed := make(map[string]*packageData)
	for _, file := range archive.File {
		if !strings.HasPrefix(file.Name, prefix+"/") {
			continue
		}
		relative := strings.TrimPrefix(file.Name, prefix+"/")
		if !isPackageFile(relative) {
			continue
		}
		dir := path.Dir(relative)
		if dir == "." {
			dir = ""
		}
		packagePath := path.Join(modulePath, dir)
		data := indexed[packagePath]
		if data == nil {
			data = &packageData{path: packagePath, dir: dir, imports: make(map[string]bool)}
			indexed[packagePath] = data
		}
		name, synopsis, imports := packageMetadata(file)
		if data.name == "" {
			data.name = name
		}
		if synopsis != "" && (data.synopsis == "" || path.Base(relative) == "doc.go") {
			data.synopsis = synopsis
		}
		for _, imported := range imports {
			data.imports[imported] = true
		}
	}

	dirs := make([]string, 0, len(indexed))
	for _, data := range indexed {
		dirs = append(dirs, data.dir)
	}
	licenseTypes := pkgsiteembed.DetectPackageLicenses(modulePath, version, &archive.Reader, dirs)
	packages := make([]Package, 0, len(indexed))
	for _, data := range indexed {
		if data.name == "" {
			data.name = path.Base(data.path)
		}
		imports := make([]string, 0, len(data.imports))
		for imported := range data.imports {
			imports = append(imports, imported)
		}
		packages = append(packages, Package{
			Name:       data.name,
			Path:       data.path,
			ModulePath: modulePath,
			Version:    version,
			Synopsis:   data.synopsis,
			CommitTime: commitTime,
			Licenses:   licenseTypes[data.dir],
			imports:    imports,
		})
	}
	return packages, nil
}

func packageMetadata(file *zip.File) (name, synopsis string, imports []string) {
	reader, err := file.Open()
	if err != nil {
		return "", "", nil
	}
	defer reader.Close()
	parsed, err := parser.ParseFile(token.NewFileSet(), file.Name, reader, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		return "", "", nil
	}
	name = parsed.Name.Name
	if parsed.Doc != nil {
		synopsis = doc.Synopsis(parsed.Doc.Text())
	}
	for _, imported := range parsed.Imports {
		importPath, err := strconv.Unquote(imported.Path.Value)
		if err == nil && importPath != "C" {
			imports = append(imports, importPath)
		}
	}
	return name, synopsis, imports
}

func populateImportedBy(packages []Package) {
	known := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		known[pkg.Path] = true
	}
	importers := make(map[string]map[string]bool)
	for _, pkg := range packages {
		for _, imported := range pkg.imports {
			if !known[imported] {
				continue
			}
			if importers[imported] == nil {
				importers[imported] = make(map[string]bool)
			}
			importers[imported][pkg.Path] = true
		}
	}
	for i := range packages {
		for importer := range importers[packages[i].Path] {
			packages[i].ImportedBy = append(packages[i].ImportedBy, importer)
		}
		sort.Strings(packages[i].ImportedBy)
		packages[i].NumImportedBy = uint64(len(packages[i].ImportedBy))
		packages[i].imports = nil
	}
}

func isPackageFile(name string) bool {
	if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		return false
	}
	dir := path.Dir(name)
	if dir == "." {
		return true
	}
	for _, part := range strings.Split(dir, "/") {
		if part == "vendor" || part == "testdata" || strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_") {
			return false
		}
	}
	return true
}

func (s *Store) serveInfo(w http.ResponseWriter, r *http.Request, version *storedVersion) {
	if info, err := os.Stat(version.info); err == nil && !info.IsDir() {
		serveFile(w, r, "application/json", version.info)
		return
	}
	data, err := json.Marshal(versionInfo{Version: version.version, Time: version.modTime.UTC()})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}

func serveFile(w http.ResponseWriter, r *http.Request, contentType, name string) {
	file, err := os.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, filepath.Base(name), info.ModTime(), file)
}

func ignoredDir(name string) bool {
	return name == "vendor" || name == "third_party" || name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

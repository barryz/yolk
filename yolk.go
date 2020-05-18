package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

const (
	// printer config
	tabWidth    = 8
	printerMode = printer.UseSpaces | printer.TabIndent

	chmodSupported = runtime.GOOS != "windows"
)

var (
	dir               = flag.String("d", "./", "source code directory which to handle")
	source            = flag.String("s", "", "source import path which to replace")
	dest              = flag.String("r", "", "destination import path which to replace")
	codeSuffixSkipped = []string{"pb.go", "pb.gopherjs.go", "stateGen.go", "reactGen.go"}
)

var replaceRules = map[string]string{}

type replacer struct {
	name    string
	oldPath string
	newPath string
}

func init() {
	log.SetFlags(log.Lshortfile | log.Ldate | log.Ltime)

	flag.Usage = usage
	flag.Parse()
}

func usage() {
	fmt.Fprint(os.Stderr, "yolk is go source code import statement modifier\n")
	fmt.Fprint(os.Stderr, "Usage: yolk [options]\n")
	fmt.Fprint(os.Stderr, "Options: \n")
	fmt.Fprint(os.Stderr, "-d   source code directory which to handle\n")
	fmt.Fprint(os.Stderr, "-s   source import path which to replace\n")
	fmt.Fprint(os.Stderr, "-r   destination import path which to replace\n")
	os.Exit(0)
}

func exitOnErr(err error) {
	log.Println(err)
	os.Exit(255)
}

var handle = func(path string, info os.FileInfo, errx error) error {
	if errx != nil {
		return errx
	}

	if info.IsDir() {
		return nil
	}

	if strings.Contains(path, "vendor") {
		return nil
	}

	filename := info.Name()
	if !strings.HasSuffix(filename, ".go") {
		return nil
	}

	for _, skip := range codeSuffixSkipped {
		if strings.HasSuffix(filename, skip) {
			return nil
		}
	}

	if err := rewriteImport(path); err != nil {
		return err
	}

	return nil
}

func importPath(s *ast.ImportSpec) string {
	t, err := strconv.Unquote(s.Path.Value)
	if err != nil {
		return ""
	}
	return t
}

func importName(s *ast.ImportSpec) string {
	if s.Name == nil {
		return ""
	}
	return s.Name.Name
}

func rewriteImport(path string) error {
	var errx error
	defer func() {
		if errx != nil {
			log.Printf("rewrite import fails with %s due to %s", path, errx)
		}
	}()

	f, err := os.Open(path)
	if err != nil {
		errx = err
		return nil
	}
	defer f.Close()

	var perm os.FileMode
	if fi, err := f.Stat(); err == nil {
		perm = fi.Mode().Perm()
	} else {
		errx = err
		return nil
	}

	src, err := ioutil.ReadAll(f)
	if err != nil {
		errx = err
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		errx = err
		return nil
	}

	replacers := make([]*replacer, 0)
	imports := astutil.Imports(fset, file)
	for _, grp := range imports {
		for _, imp := range grp {
			impPath := importPath(imp)
			for pre, will := range replaceRules {
				if strings.HasPrefix(impPath, pre) {
					np := fmt.Sprintf("%s%s", will, strings.TrimPrefix(impPath, pre))
					op := impPath
					replacer := &replacer{oldPath: op, newPath: np, name: importName(imp)}
					replacers = append(replacers, replacer)
				}
			}

		}
	}

	for _, r := range replacers {
		if !astutil.DeleteNamedImport(fset, file, r.name, r.oldPath) {
			errx = fmt.Errorf("delete old path fails")
			return nil
		}

		if !astutil.AddNamedImport(fset, file, r.name, r.newPath) {
			errx = fmt.Errorf("add new path fails")
			return nil
		}
	}

	var dst bytes.Buffer
	cfg := printer.Config{Mode: printerMode, Tabwidth: tabWidth}
	if err := cfg.Fprint(&dst, fset, file); err != nil {
		errx = err
		return nil
	}

	bs, err := format.Source(dst.Bytes())
	if err != nil {
		errx = err
		return nil
	}

	// backup first
	backname, err := backupFile(path+".", src, perm)
	if err != nil {
		errx = err
		return nil
	}

	// write content to file
	if err := ioutil.WriteFile(path, bs, perm); err != nil {
		os.Rename(backname, path)
		errx = err
		return nil
	}

	// delete backup file
	if err := os.Remove(backname); err != nil {
		errx = err
		return nil
	}

	return nil
}

func backupFile(filename string, data []byte, perm os.FileMode) (string, error) {
	backfile, err := ioutil.TempFile(filepath.Dir(filename), filepath.Base(filename))
	if err != nil {
		return "", err
	}

	backname := backfile.Name()

	if chmodSupported {
		err = backfile.Chmod(perm)
		if err != nil {
			backfile.Close()
			os.Remove(backname)
			return backname, err
		}
	}

	if _, err := backfile.Write(data); err != nil {
		return backname, err
	}

	if err := backfile.Close(); err != nil {
		return backname, err
	}

	return backname, nil

}

func initReplaceRules() {
	replaceRules = map[string]string{
		*source: *dest,
	}
}

func main() {
	if *dir == "" {
		exitOnErr(fmt.Errorf("you must specify a directory to handle"))
	}

	if *source == "" || *dest == "" {
		exitOnErr(fmt.Errorf("you must specify a source or destination import path to handle"))
	}

	if err := filepath.Walk(*dir, handle); err != nil {
		exitOnErr(err)
	}
}

package doctor

import (
	"bytes"
	"code.google.com/p/go.tools/go/types"
	//"fmt"
	"go/ast"
	"go/token"
	//"reflect"
	"sort"
	"strings"
)

// This file defines a Fix Imports transformation, which removes unnecessary
// imports and adds imports (if possible) for unresolved identifiers that
// match a package name and are used in a selector context.  This is heuristic
// -- it doesn't do any deep analysis to determine whether the new imports will
// fix any errors, and it doesn't determine whether the new imported packages
// "make sense" in terms of entities referenced.
//
// (Mainly, I wanted to be able to add/remove calls to fmt.Println and
// reflect.TypeOf without having to manually edit the import block repeatedly.)
//
// This is not called "FixImportsRefactoring" since it does not technically
// preserve behavior.

type FixImportsTransformation struct {
	RefactoringBase
}

func (r *FixImportsTransformation) Name() string {
	return "Fix Imports"
}

func (r *FixImportsTransformation) Configure(args []string) bool {
	return true
}

func (r *FixImportsTransformation) GetParams() []string {
	return []string{}
}

func (r *FixImportsTransformation) Run() {
	if r.file == nil {
		r.log.Log(FATAL_ERROR, "file cannot be null")
		return // SetSelection did not succeed
	}

	r.log.RemoveInitialErrors()

	//ast.Print(r.importer.Fset, r.file)

	imports := r.findUsedImports()

	for _, ident := range r.file.Unresolved {
		resolved := r.resolve(ident)
		if resolved != "" {
			imports[resolved] = ""
		}
	}

	r.fixImports(imports)
}

func (r *FixImportsTransformation) findUsedImports() map[string]string {
	// Determine which package names are actually referenced
	packagesReferenced := r.collectReferencedPackages()

	// Collect all existing imports, omitted unreferenced packages
	imports := map[string]string{}
	for _, imprt := range r.file.Imports {
		var path string = imprt.Path.Value
		var name string = ""
		if imprt.Name != nil {
			name = imprt.Name.Name
		}
		_, foundName := packagesReferenced[name]
		_, foundPkg := packagesReferenced[path]
		if foundName || foundPkg {
			imports[path] = name
		}
	}

	return imports
}

func (r *FixImportsTransformation) collectReferencedPackages() map[string]string {
	packagesReferenced := map[string]string{}
	ast.Inspect(r.file, func(n ast.Node) bool {
		switch ident := n.(type) {
		case *ast.Ident:
			decl := r.pkgInfo.ObjectOf(ident)
			//fmt.Println("ObjectOf", ident, "is", decl)
			if decl != nil {
				//fmt.Println(reflect.TypeOf(decl))
				switch pkgName := decl.(type) {
				case *types.PkgName:
					packagesReferenced[pkgName.Name()] = ""
				}
			}
		}
		return true
	})
	return packagesReferenced
}

func (r *FixImportsTransformation) resolve(ident *ast.Ident) string {
	if r.isIdentInLHSOfSelectorExpr(ident) {
		return r.resolveSelector(ident)
	} else {
		r.log.Log(ERROR, "Unable to resolve "+ident.Name)
		return ""
	}
}

func (r *FixImportsTransformation) isIdentInLHSOfSelectorExpr(ident *ast.Ident) bool {
	result := false
	ast.Inspect(r.file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			switch node.X.(type) {
			case *ast.Ident:
				result = true
				return false
			}
		}
		return true
	})
	return result
}

func (r *FixImportsTransformation) resolveSelector(ident *ast.Ident) string {
	var candidates []string = []string{}
	for _, pkg := range goLibraryPackages {
		components := strings.Split(pkg, "/")
		last := components[len(components)-1]
		if last == ident.Name {
			candidates = append(candidates, pkg)
		}
	}
	if len(candidates) == 1 {
		return "\"" + candidates[0] + "\""
	} else {
		// TODO: Could look at what methods are invoked, etc. to
		// attempt to resolve this
		var message bytes.Buffer
		message.WriteString("There are multiple packages named ")
		message.WriteString(ident.Name)
		message.WriteString(":\n")
		for _, candidate := range candidates {
			message.WriteString("    " + candidate + "\n")
		}
		r.log.Log(ERROR, message.String())
		return ""
	}
}

func (r *FixImportsTransformation) fixImports(imports map[string]string) {
	replaceRange, suffix := r.findReplacementRange()
	replacement := r.constructNewImportStatement(imports) + suffix
	r.editSet.Add(r.filename, replaceRange, replacement)
}

func (r *FixImportsTransformation) findReplacementRange() (OffsetLength, string) {
	if len(r.file.Imports) == 0 {
		return OffsetLength{r.findFirstDeclOffset(), 0}, "\n"
	} else {
		return r.findImportStatementRange(), ""
	}
}

func (r *FixImportsTransformation) findFirstDeclOffset() int {
	var pos token.Pos = r.file.Decls[0].Pos()
	return r.importer.Fset.Position(pos).Offset
}

func (r *FixImportsTransformation) findImportStatementRange() OffsetLength {
	var startPos token.Pos = 0
	var endPos token.Pos = 0
	ast.Inspect(r.file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.GenDecl:
			if node.Tok == token.IMPORT {
				if startPos == 0 {
					startPos = node.Pos()
				}
				endPos = node.End()
			}
		}
		return true
	})

	startOffset := r.importer.Fset.Position(startPos).Offset
	endOffset := r.importer.Fset.Position(endPos).Offset
	length := endOffset - startOffset + 1
	return OffsetLength{startOffset, length}
}

func (r *FixImportsTransformation) constructNewImportStatement(importSet map[string]string) string {
	// Construct the lines of the new import statement from importSet
	imports := []string{}
	for path, name := range importSet {
		var thisImport string
		if name == "" {
			thisImport = path
		} else {
			thisImport = name + " " + path
		}
		imports = append(imports, thisImport)
	}
	sort.Strings(imports)

	// Construct the import statement
	var buffer bytes.Buffer
	buffer.WriteString("import (\n")
	for _, line := range imports {
		buffer.WriteString("\t")
		buffer.WriteString(line)
		buffer.WriteString("\n")
	}
	buffer.WriteString(")\n")
	return buffer.String()
}

var goLibraryPackages []string = []string{
	"archive",
	"archive/tar",
	"archive/zip",
	"bufio",
	"builtin",
	"bytes",
	"compress",
	"compress/bzip2",
	"compress/flate",
	"compress/gzip",
	"compress/lzw",
	"compress/zlib",
	"container",
	"container/heap",
	"container/list",
	"container/ring",
	"crypto",
	"crypto/aes",
	"crypto/cipher",
	"crypto/des",
	"crypto/dsa",
	"crypto/ecdsa",
	"crypto/elliptic",
	"crypto/hmac",
	"crypto/md5",
	"crypto/rand",
	"crypto/rc4",
	"crypto/rsa",
	"crypto/sha1",
	"crypto/sha256",
	"crypto/sha512",
	"crypto/subtle",
	"crypto/tls",
	"crypto/x509",
	"crypto/x509/pkix",
	"database",
	"database/sql",
	"database/sql/driver",
	"debug",
	"debug/dwarf",
	"debug/elf",
	"debug/gosym",
	"debug/macho",
	"debug/pe",
	"encoding",
	"encoding/ascii85",
	"encoding/asn1",
	"encoding/base32",
	"encoding/base64",
	"encoding/binary",
	"encoding/csv",
	"encoding/gob",
	"encoding/hex",
	"encoding/json",
	"encoding/pem",
	"encoding/xml",
	"errors",
	"expvar",
	"flag",
	"fmt",
	"go",
	"go/ast",
	"go/build",
	"go/doc",
	"go/format",
	"go/parser",
	"go/printer",
	"go/scanner",
	"go/token",
	"hash",
	"hash/adler32",
	"hash/crc32",
	"hash/crc64",
	"hash/fnv",
	"html",
	"html/template",
	"image",
	"image/color",
	"image/color/palette",
	"image/draw",
	"image/gif",
	"image/jpeg",
	"image/png",
	"index",
	"index/suffixarray",
	"io",
	"io/ioutil",
	"log",
	"log/syslog",
	"math",
	"math/big",
	"math/cmplx",
	"math/rand",
	"mime",
	"mime/multipart",
	"net",
	"net/http",
	"net/http/cgi",
	"net/http/cookiejar",
	"net/http/fcgi",
	"net/http/httptest",
	"net/http/httputil",
	"net/http/pprof",
	"net/mail",
	"net/rpc",
	"net/rpc/jsonrpc",
	"net/smtp",
	"net/textproto",
	"net/url",
	"os",
	"os/exec",
	"os/signal",
	"os/user",
	"path",
	"path/filepath",
	"reflect",
	"regexp",
	"regexp/syntax",
	"runtime",
	"runtime/cgo",
	"runtime/debug",
	"runtime/pprof",
	"runtime/race",
	"sort",
	"strconv",
	"strings",
	"sync",
	"sync/atomic",
	"syscall",
	"testing",
	"testing/iotest",
	"testing/quick",
	"text",
	"text/scanner",
	"text/tabwriter",
	"text/template",
	"text/template/parse",
	"time",
	"unicode",
	"unicode/utf16",
	"unicode/utf8",
	"unsafe",
}

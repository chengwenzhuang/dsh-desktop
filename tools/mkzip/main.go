// mkzip zips a directory tree into a single zip (deflate), storing
// forward-slash relative paths. Used to build the embedded dsh runtime.
//
// Usage: go run ./tools/mkzip -src <dir> -out <file.zip>
package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

func main() {
	src := flag.String("src", "", "source directory")
	out := flag.String("out", "", "output zip path")
	flag.Parse()
	if *src == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: mkzip -src <dir> -out <file.zip>")
		os.Exit(2)
	}
	absSrc, err := filepath.Abs(*src)
	if err != nil {
		panic(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		panic(err)
	}
	zw := zip.NewWriter(f)

	var count atomic.Int64
	var total atomic.Int64
	err = filepath.Walk(absSrc, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(absSrc, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		rf, err := os.Open(path)
		if err != nil {
			return err
		}
		defer rf.Close()
		n, err := io.Copy(w, rf)
		if err != nil {
			return err
		}
		total.Add(n)
		if c := count.Add(1); c%2000 == 0 {
			fmt.Fprintf(os.Stderr, "  zipped %d files (%d MB)...\n", c, total.Load()/1<<20)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	if err := f.Close(); err != nil {
		panic(err)
	}
	st, _ := os.Stat(*out)
	fmt.Printf("done: %d files, %.1f MB raw -> %.1f MB zip\n", count.Load(),
		float64(total.Load())/(1<<20), float64(st.Size())/(1<<20))
	_ = strings.Builder{}
}

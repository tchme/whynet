// Command adlist rebuilds rules/ads.txt.gz, the built-in list of ad and tracker domains,
// from the StevenBlack/hosts unified list (MIT). Run from the repo root:
//
//	go run ./tools/adlist                      # download the current list
//	go run ./tools/adlist -src path/to/hosts   # or use a file already downloaded
package main

import (
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

const upstream = "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts"

func main() {
	src := flag.String("src", upstream, "hosts file: URL or local path")
	out := flag.String("out", "rules/ads.txt.gz", "output file")
	flag.Parse()
	if err := run(*src, *out); err != nil {
		fmt.Fprintln(os.Stderr, "adlist:", err)
		os.Exit(1)
	}
}

func run(src, out string) error {
	var r io.Reader
	if strings.HasPrefix(src, "https://") {
		cl := &http.Client{Timeout: 2 * time.Minute}
		resp, err := cl.Get(src)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("%s: HTTP %d", src, resp.StatusCode)
		}
		r = resp.Body
	} else {
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}

	var domains []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		f := strings.Fields(line)
		// "0.0.0.0 ads.example.com"; the file's own localhost lines are not ads
		if len(f) == 2 && f[0] == "0.0.0.0" && f[1] != "0.0.0.0" && strings.Contains(f[1], ".") {
			domains = append(domains, strings.ToLower(f[1]))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	slices.Sort(domains)
	domains = slices.Compact(domains)
	if len(domains) < 10000 {
		return fmt.Errorf("only %d domains: not the list we expect", len(domains))
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	zw, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
	zw.Write([]byte(strings.Join(domains, "\n") + "\n"))
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("%d domains -> %s\n", len(domains), out)
	return nil
}

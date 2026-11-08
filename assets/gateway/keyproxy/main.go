// Command keyproxy runs in the gateway container and adds the OpenRouter key
// to requests from the workspace. It is stdlib-only so the gateway image can
// build it from the embedded assets without the rest of this module.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:4000", "listen address (keep it on loopback)")
	upstream := flag.String("upstream", "https://openrouter.ai", "upstream base URL")
	keyFile := flag.String("key-file", "/var/lib/sandbox-secrets/openrouter", "file holding the API key")
	store := flag.Bool("store", false, "read a key from stdin, save it to -key-file, and exit")
	flag.Parse()

	if *store {
		if err := StoreSecret(*keyFile, os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, "keyproxy:", err)
			os.Exit(1)
		}
		return
	}

	u, err := url.Parse(*upstream)
	if err != nil || u.Scheme == "" || u.Host == "" {
		log.Fatalf("keyproxy: bad -upstream %q", *upstream)
	}
	srv := &http.Server{
		Addr:              *listen,
		Handler:           New(u, *keyFile),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("keyproxy: %s -> %s", *listen, u)
	log.Fatal(srv.ListenAndServe())
}

// Command doliconvert regenerates ForgeERP locale catalogue JSON from a
// vendored Dolibarr tree. New locale = new Target entry in the convert
// package + rerun + speaker review before commit.
//
//	go run ./backend/internal/platform/locale/convert/cmd/doliconvert \
//	  -langs .forgeerp-temp/dolibarr/htdocs/langs \
//	  -out backend/internal/platform/locale/locales
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/locale/convert"
)

func main() {
	langs := flag.String("langs", ".forgeerp-temp/dolibarr/htdocs/langs", "Dolibarr htdocs/langs root")
	out := flag.String("out", "backend/internal/platform/locale/locales", "catalogue JSON output dir")
	flag.Parse()
	if err := convert.ConvertLangs(*langs, *out, convert.DefaultTargets); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, t := range convert.DefaultTargets {
		fmt.Printf("wrote %s/%s.json\n", *out, t.Tag)
	}
}

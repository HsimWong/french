//go:build !noseed

package main

import (
	_ "embed"
	"os"
)

// The dictionary seed is compiled into the binary by default, which keeps a
// deployment to a single file. The trade-offs are real: the build needs the
// 160+ MB data/lexicon_seed.json to be present in the source tree, and the
// resulting binary is 240+ MB.
//
// Build with `-tags noseed` to leave the dictionary out and read it from disk
// instead; see lexicon_seed_file.go.
//
//go:embed data/lexicon_seed.json
var embeddedLexiconSeedJSON []byte

// defaultLexiconSeed decides where the dictionary comes from.
// LEXICON_SEED_FILE overrides the compiled-in copy, which is handy when the
// seed is refreshed without rebuilding the whole binary.
func defaultLexiconSeed() lexiconSeedSource {
	if path := os.Getenv("LEXICON_SEED_FILE"); path != "" {
		return fileLexiconSeed{path: path}
	}
	return embeddedLexiconSeed{data: embeddedLexiconSeedJSON}
}

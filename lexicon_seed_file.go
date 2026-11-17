//go:build noseed

package main

import "os"

// defaultLexiconSeedPath is where the dictionary is expected when the binary
// is built with `-tags noseed`.
const defaultLexiconSeedPath = "data/lexicon_seed.json"

// defaultLexiconSeed reads the dictionary from disk.
//
// The `noseed` build produces a ~80 MB binary that can be compiled and run on
// a machine that does not have the 160+ MB seed file checked out, and the
// seed is streamed straight into the database instead of being materialised
// in memory.
//
// LEXICON_SEED_FILE overrides the default path.
func defaultLexiconSeed() lexiconSeedSource {
	path := os.Getenv("LEXICON_SEED_FILE")
	if path == "" {
		path = defaultLexiconSeedPath
	}
	return fileLexiconSeed{path: path}
}

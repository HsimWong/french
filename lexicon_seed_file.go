package main

import "os"

// defaultLexiconSeedPath is where the dictionary is expected by default.
// The seed is a runtime data file next to the binary, not part of it: putting
// 164 MB of JSON into the binary made cold builds on a small VPS take many
// minutes and blow up memory, for no benefit.
const defaultLexiconSeedPath = "data/lexicon_seed.json"

// defaultLexiconSeed reads the dictionary from disk.
//
// The seed is streamed into the database one array element at a time, so peak
// heap stays in the low megabytes regardless of file size. Import happens only
// when the stored fingerprint differs, so normal restarts do not touch it.
//
// LEXICON_SEED_FILE overrides the default path.
func defaultLexiconSeed() lexiconSeedSource {
	path := os.Getenv("LEXICON_SEED_FILE")
	if path == "" {
		path = defaultLexiconSeedPath
	}
	return fileLexiconSeed{path: path}
}

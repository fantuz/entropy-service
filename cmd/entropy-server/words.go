package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/8ff/diceware"
)

// Parse the dictionary once; sampling never shuffles or mutates it.
var dicewareWords = diceware.GetWords()

func newWordFrame(source io.Reader, count int) (EntropyFrame, error) {
	if count < 1 || len(dicewareWords) == 0 {
		return EntropyFrame{}, errors.New("invalid word count or empty dictionary")
	}

	words := make([]string, count)
	limit := big.NewInt(int64(len(dicewareWords)))

	for i := range words {
		n, err := rand.Int(source, limit)
		if err != nil {
			return EntropyFrame{}, fmt.Errorf("select word: %w", err)
		}

		words[i] = dicewareWords[n.Int64()]
	}

	phrase := strings.Join(words, " ")
	hash := sha256.Sum256([]byte(phrase))

	return EntropyFrame{Words: phrase, Hash: hex.EncodeToString(hash[:])}, nil
}

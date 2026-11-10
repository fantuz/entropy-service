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

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/wordlist"
)

func newWordFrame(source io.Reader, count int) (EntropyFrame, error) {
	return newWordFrameFromList(source, count, wordlist.Default())
}

func newWordFrameFromList(source io.Reader, count int, dictionary *wordlist.Dictionary) (EntropyFrame, error) {
	if count < 1 || dictionary == nil {
		return EntropyFrame{}, errors.New("invalid word count or empty dictionary")
	}

	words := make([]string, count)
	limit := big.NewInt(int64(dictionary.Size()))

	for i := range words {
		n, err := rand.Int(source, limit)
		if err != nil {
			return EntropyFrame{}, fmt.Errorf("select word: %w", err)
		}

		words[i] = dictionary.Word(int(n.Int64()))
	}

	phrase := strings.Join(words, " ")
	hash := sha256.Sum256([]byte(phrase))

	info := dictionary.Info()

	return EntropyFrame{Words: phrase, Hash: hex.EncodeToString(hash[:]), Language: info.Code, Wordlist: info.ID}, nil
}

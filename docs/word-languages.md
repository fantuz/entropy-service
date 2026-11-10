# Word languages, dictionaries and selection space

`/words` now accepts `lang=CODE`. The browser at `/api-words.html` offers the
same ten languages. `/words.html` redirects there, retaining query values.
HTTP word responses at `/v1/meta/random` and `/paroleparoleparole` also accept
the parameter. The interface labels remain English; the generated words use
the selected language.

| Code | Language | Entries | Ideal selection bits per word | Published word data |
| --- | --- | ---: | ---: | --- |
| `en` | English | 7,776 | 12.925 | EFF long passphrase list |
| `it` | Italian | 8,192 | 13 | Italian Diceware list |
| `fr` | French | 7,776 | 12.925 | Tails French Diceware list |
| `de` | German | 7,776 | 12.925 | German Diceware list |
| `es` | Spanish | 8,192 | 13 | Spanish Diceware list |
| `pt-BR` | Portuguese (Brazil) | 7,776 | 12.925 | Brazilian Portuguese Diceware list |
| `ca` | Catalan | 8,192 | 13 | Catalan Diceware list |
| `cs` | Czech | 2,048 | 11 | Trezor BIP39 dictionary, reused as word data |
| `ja` | Japanese | 2,048 | 11 | Trezor BIP39 dictionary, reused as word data |
| `ko` | Korean | 2,048 | 11 | Trezor BIP39 dictionary, reused as word data |

English remains the default. Its entries and ordering exactly match the
previous EFF-derived dictionary. The `github.com/8ff/diceware` dependency has
been removed; the published data is now embedded directly with its provenance
and license. No dictionary is fetched while serving a request.

## Diceware, translation and “validated” words

Diceware is a method for selecting words independently from a known list. It
does not define a universal multilingual passphrase certification. The EFF
long list is specifically curated for passphrases; other language lists have
their own authors and selection criteria. BIP39 dictionaries are published for
wallet mnemonics. Here they supply **word data only**: this service does not
implement BIP39 entropy encoding, checksums, mnemonic validation or wallet seeds.

Translating a generated phrase is not equivalent to generating from another
language's dictionary. Several source words can translate to the same output,
or to multiple words, changing both the number and probability of results.
A reviewed one-to-one mapping could preserve the selection space, but ordinary
translation does not establish that property. Adding a new language therefore
means reviewing a native list, attribution, normalization, duplicates and size,
rather than passing generated phrases to a translation service.

The ten lists have different sizes. For `w` independent uniform selections from
`N` distinct entries, the ideal choice space is `N^w`, or `w × log2(N)` bits.
Six EFF words provide about 77.55 bits of choices; six 2,048-entry words provide
66. Eight words from a 2,048-entry list exceed the choice space of six EFF words.
The language selector preserves your requested word count, so it also displays
the selected list size and bits per word. Existing server word defaults/limits
remain controlled by `-words` (factory default/limit 20).

These figures describe dictionary choices under uniform selection. They do not
measure the seed source or independently validate the project's DRBG. The
[existing cryptographic boundaries](../SECURITY.md#source-and-drbg-boundaries)
continue to apply. Repetitions are valid: sampling is with replacement, using
the existing unbiased `crypto/rand.Int` rejection sampling over the supplied
generator, without word shuffling or modulo bias.

## Requests and responses

```text
ws://127.0.0.1:8080/words?lang=it&words=6&refresh=1000
http://127.0.0.1:8080/api-words.html?lang=ja&words=8&refresh=500
http://127.0.0.1:8080/words.html?lang=ko&words=8
http://127.0.0.1:8080/v1/meta/random?lang=fr&words=6
```

Codes are case-insensitive; `pt` is an alias of `pt-BR`. Missing `lang` means
`en`. Empty, duplicate, unsupported and malformed language queries return 400
before generation/admission. Unknown regional tags are not silently replaced
with English. Language is per request/connection and does not change shared
state or the numeric preference cookie. The browser allows correcting an
invalid language selection without opening an English stream automatically.

WebSocket JSON retains `words` and `hash` and adds:

- `language`: canonical language code.
- `wordlist`: stable identifier derived from the normalized dictionary checksum.

The SHA-256 covers exactly the UTF-8 bytes of the space-separated phrase,
without a final newline. All embedded entries are Unicode NFC, including
Japanese/Korean entries whose upstream BIP39 data uses decomposed forms.
Separators are ASCII spaces in every language. This normalization is disclosed
as a data transformation; the dictionaries are not used as BIP39 protocol data.

HTTP HTML adds `Content-Language`, `X-Wordlist-ID`, `X-Wordlist-Size` and
`Cache-Control: no-store`. Its word element has the appropriate HTML `lang`.
Metadata is available through `/wordlists.json`, including source revisions,
checksums, entry counts, selection bits and licenses. It supports ETag
revalidation and consumes no generator output. `/wordlist-notices.txt` contains
the corresponding attribution and license notices.

Memory reservations use the longest **UTF-8 byte length across all lists**,
including multibyte scripts. Request limits, source-freshness gating,
WebSocket cleanup and display pacing remain in force.

## Provenance, licensing and updates

The [manifest](../cmd/entropy-server/internal/wordlist/manifest.json) records
each exact upstream revision, original SHA-256, derived SHA-256, size, author
and license. The first seven lists come from the attributed word-data assets
in `ulif/diceware`; the Czech/Japanese/Korean lists come from
`trezor/python-mnemonic`. No upstream generator implementation is copied.
Data retains its CC BY 3.0, CC BY 4.0, CC0 or MIT terms; the application's own
code license does not relabel it. Full credits, license links and the required
MIT notice are in [NOTICE.txt](../cmd/entropy-server/internal/wordlist/NOTICE.txt).

Release archives include `WORDLIST_NOTICES.txt`; DEB/RPM packages include
`/usr/share/doc/entropy-service/wordlists-copyright`. The same notice is embedded
in the standalone server and served at `/wordlist-notices.txt`.

```sh
# Offline integrity, NFC, uniqueness, attribution and size checks:
python3 scripts/check-wordlists.py

# Explicitly reproduce from the pinned upstream URLs and verify both hashes:
python3 scripts/check-wordlists.py --import
```

Import removes dice indexes where present, normalizes NFC and emits one word
per LF-terminated UTF-8 line, preserving order. It never translates, filters
or silently deduplicates. Updating a list requires an explicit source/hash
review and a changed list identifier. Structural checks do not replace native
linguistic review or establish that every published list has equal usability.

Focused Go tests cover rejection sampling, repeated words, all ten HTTP/WS
languages, exact Unicode hashes, request independence, invalid queries,
UTF-8 memory bounds and catalog/redirect/notice behavior. Node tests cover
selection, parameter preservation, text safety, response identity and cleanup.
Build and release CI run the dictionary and browser-control checks alongside
the full regression suite.

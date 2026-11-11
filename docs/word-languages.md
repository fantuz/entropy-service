# Word languages, dictionaries and selection space

`/words` now accepts `lang=CODE`. The browser at `/api-words.html` offers the
same 18 dictionary choices across 17 languages, including both Chinese scripts.
`/words.html` redirects there, retaining query values.
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
| `ru` | Russian | 2,048 | 11 | Trezor BIP39 word data |
| `af` | Afrikaans | 8,192 | 13 | Native spelling dictionary subset |
| `tr` | Turkish | 2,048 | 11 | Trezor BIP39 word data |
| `zh-Hans` | Chinese (Simplified) | 2,048 | 11 | Trezor BIP39 character data |
| `zh-Hant` | Chinese (Traditional) | 2,048 | 11 | Trezor BIP39 character data |
| `ta` | Tamil | 8,192 | 13 | Native spelling dictionary subset |
| `ne` | Nepali | 8,192 | 13 | Native spelling dictionary subset |
| `ar` | Arabic | 8,192 | 13 | Native spelling dictionary subset |

English remains the default. All original ten dictionaries retain their data,
ordering and identifiers. English exactly matches the previous EFF-derived
dictionary. The `github.com/8ff/diceware` dependency has
been removed; the published data is now embedded directly with its provenance
and license. No dictionary is fetched while serving a request.

## Diceware, translation and “validated” words

Diceware is a method for selecting words independently from a known list. It
does not define a universal multilingual passphrase certification. The EFF
long list is specifically curated for passphrases; other language lists have
their own authors and selection criteria. BIP39 dictionaries are published for
wallet mnemonics. Here they supply **word data only**: this service does not
implement BIP39 entropy encoding, checksums, mnemonic validation or wallet seeds.
Chinese entries are individual characters, with the same ASCII-space separator
as the other dictionaries. Arabic words retain their diacritics and display
with automatic text direction; the adjacent hash remains separate.

Afrikaans, Tamil, Nepali and Arabic use reproducible subsets of native spelling
dictionaries distributed by LibreOffice. They are **not curated passphrase
lists**. They may contain compounds, inflections, uncommon vocabulary or
sensitive terms. Native linguistic and usability review remains useful; size,
uniqueness and encoding checks do not establish memorability or certification.

Translating a generated phrase is not equivalent to generating from another
language's dictionary. Several source words can translate to the same output,
or to multiple words, changing both the number and probability of results.
A reviewed one-to-one mapping could preserve the selection space, but ordinary
translation does not establish that property. Adding a new language therefore
means reviewing a native list, attribution, normalization, duplicates and size,
rather than passing generated phrases to a translation service.

The lists have different sizes. For `w` independent uniform selections from
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
http://127.0.0.1:8080/words.html?lang=ru&words=8
http://127.0.0.1:8080/words.html?lang=af&words=6
http://127.0.0.1:8080/words.html?lang=ta&words=6
http://127.0.0.1:8080/words.html?lang=ar&words=6
http://127.0.0.1:8080/words.html?lang=zh-TW&words=8
```

Codes are case-insensitive. Aliases resolve to the same canonical dictionary:

| Alias | Canonical code |
| --- | --- |
| `pt` | `pt-BR` |
| `zh`, `zh-CN`, `zh-SG` | `zh-Hans` |
| `zh-TW`, `zh-HK`, `zh-MO` | `zh-Hant` |

Missing `lang` means `en`, or the validated remembered language when
`-cookie=true`. Empty, duplicate, unsupported and malformed language queries return 400
before generation/admission. Unknown regional tags are not silently replaced
with English. Language is per request/connection and does not change another
client's selection. The browser allows correcting an
invalid language selection without opening an English stream automatically.
With cookies enabled, successful HTTP preference responses and `/words`
WebSocket upgrades save the canonical `lang`. The browser catalog supplies the
remembered choice without exposing the HttpOnly cookie to JavaScript. Explicit
query values win. Without cookies, new requests default to English; bookmarked
URLs still carry their selection. See the [encoding and cookie policy](configuration.md#preference-cookies).

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
checksums, entry counts, selection bits, aliases and licenses. Without cookies
it supports ETag revalidation. With cookies, `default` stays `en` and `selected`
contains the validated remembered language; the response uses `private, no-store`
and `Vary: Cookie`. Reading the catalog neither saves a cookie nor consumes
generator output. `/wordlist-notices.txt` contains
the corresponding attribution and license notices.

Memory reservations use the longest **UTF-8 byte length across all lists**,
including multibyte scripts. Request limits, source-freshness gating,
WebSocket cleanup and display pacing remain in force.

## Provenance, licensing and updates

The [manifest](../cmd/entropy-server/internal/wordlist/manifest.json) records
each exact upstream revision, original SHA-256, derived SHA-256, size, author
and license. The first seven lists come from the attributed word-data assets
in `ulif/diceware`; BIP39 word data comes from `trezor/python-mnemonic`.
Spelling subsets come from pinned LibreOffice dictionary data, originally
authored by Translate.org.za and contributors (Afrikaans), Ve. Elanjelian /
Thamizha (Tamil), Madan Puraskar Pustakalaya (Nepali) and Mohamed Kebdani /
Ayaspell (Arabic). No upstream generator implementation is copied.
Data retains its CC BY 3.0, CC BY 4.0, CC0, MIT, LGPL 2.1 or MPL 1.1 terms;
Arabic uses its offered LGPL 2.1-or-later option. The application's own
code license does not relabel it. Full credits, license links and the required
MIT, LGPL and MPL texts are in [NOTICE.txt](../cmd/entropy-server/internal/wordlist/NOTICE.txt).

Release archives include `WORDLIST_NOTICES.txt`; DEB/RPM packages include
`/usr/share/doc/entropy-service/wordlists-copyright`. The same notice is embedded
in the standalone server and served at `/wordlist-notices.txt`.

```sh
# Offline integrity, NFC, uniqueness, attribution and size checks:
python3 scripts/check-wordlists.py
python3 -m unittest discover -s scripts -p 'test_check_wordlists.py'

# Explicitly reproduce from the pinned upstream URLs and verify both hashes:
python3 scripts/check-wordlists.py --import
```

Diceware/BIP39 import removes dice indexes where present, normalizes NFC and
emits one word per LF-terminated UTF-8 line, preserving order without filtering
or deduplication. No list is translated. Spelling subsets use this explicit
versioned transformation:

1. Validate the Hunspell count and remove `/affix` flags without expanding
   inflections. The pinned Tamil source declares 83,607 records but has 83,606;
   only that specific discrepancy is accepted. Other mismatches fail import.
2. Normalize NFC and deduplicate stems. Afrikaans keeps lowercase alphabetic
   words of 4–12 code points. Tamil, Nepali and Arabic keep 3–12 code points,
   starting with a letter and containing only letters/marks in their respective
   Unicode character-name families. Whitespace, comments, digits, controls and
   joiners are excluded; diacritics are retained.
3. Rank by SHA-256 of UTF-8 `entropy-service:CODE:v1:` plus the word, with the
   word as tie-breaker; keep 8,192, then sort by Unicode code point. The ranking
   chooses a fixed public subset during import; serving still samples uniformly.

Source downloads are bounded to 16 MiB and must match the pinned raw checksum
before transformation. Derived checksums must match before files are replaced.
Updating a list requires an explicit source/hash
review and a changed list identifier. Structural checks do not replace native
linguistic review or establish that every published list has equal usability.

Focused Go tests cover rejection sampling, repeated words, all HTTP/WS dictionary
choices, exact Unicode hashes, request independence, aliases, invalid queries,
UTF-8 memory bounds, HTTPS/WSS cookie round trips and catalog/redirect/notices.
Importer tests cover native scripts, filtering, NFC, reproducible selection and
bad source counts. Node tests cover selection, remembered language, parameter
preservation, text safety, response identity and cleanup.
Build and release CI run the dictionary and browser-control checks alongside
the full regression suite.

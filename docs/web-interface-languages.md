# Web interface languages

The browser interface supports the same 14 languages as
[OneTagger's interface](https://github.com/fantuz/onetagger/blob/master/client/src/i18n/locale.ts):

| Language | Interface code |
| --- | --- |
| English | `en` |
| French | `fr` |
| German | `de` |
| Spanish | `es` |
| Czech | `cs` |
| Russian | `ru` |
| Polish | `pl` |
| Greek | `el` |
| Italian | `it` |
| Dutch | `nl` |
| Portuguese (Portugal) | `pt-PT` |
| Portuguese (Brazil) | `pt-BR` |
| Japanese | `ja` |
| Arabic | `ar` |

Arabic uses right-to-left interface direction. Code, byte dumps, fingerprints
and plotted data retain their original order. Browser language detection keeps
Brazilian and European Portuguese separate; an unqualified `pt` preference uses
European Portuguese. The language menu lists only translations available on the
selected server.

## Installation choices

The bundled Windows installer offers a component selection page with **Full
installation**, **English web interface only**, and **Custom installation**.
English and the core application are required. Custom installation lets users
select each additional translation separately. Full installation is the initial
default; subsequent setups remember the installed selection. Running setup again
can add or remove interface translations without a separate uninstall.

These choices apply to the web pages served by the installed local server.
They do not translate the native desktop GUI or the installer itself, and do not
change a remote server's language menu. The portable CLI archives, Linux/macOS
packages and containers retain all embedded interface translations.

The installer builds its server with `external_ui_locales`: English and core web
assets remain embedded, and selected JSON catalogs are installed in
`bin\ui-locales` beside `entropy-server.exe`. Discovery uses the executable's
directory, independently of the working directory. Catalogs are validated and
snapshotted at startup; adding/removing one takes effect after restarting that
server. A missing optional catalog directory leaves an English interface. A
malformed or incompatible selected catalog produces a startup error identifying
the file; reinstall matching language components to repair it.

Only known catalog filenames are loaded. The server bounds file size, checks
UTF-8, text message keys and interpolation parameters, and does not expose the
installation directory as a public file tree. Deployment changes also change the
catalog cache version, so pages reloaded after deployment receive the current
language set. Language components occupy much less space than the private Python/GTK
runtime; selection mainly controls which interface translations are available.

Silent setup examples:

```powershell
# Required application, English interface and all random-word dictionaries.
.\entropy-service_setup.exe /CURRENTUSER /VERYSILENT /NORESTART /TYPE=english /COMPONENTS=core

# Add only French and German interface translations.
.\entropy-service_setup.exe /CURRENTUSER /VERYSILENT /NORESTART /TYPE=custom "/COMPONENTS=core,web_locales,web_locales\fr,web_locales\de"

# Install every interface translation.
.\entropy-service_setup.exe /CURRENTUSER /VERYSILENT /NORESTART /TYPE=full
```

Replace the example executable name with the versioned setup file downloaded
from the release. These options do not select the optional Windows Service.
See the [installer guide](windows-gui-installer.md) for service installation and
upgrade behavior.

## Preferences and random words

The chosen interface code is stored as plain text in the browser's
`localStorage` under `entropy-ui-language`, scoped to that server origin. It is
not a cookie or a stored translation catalog. If a saved language is unavailable,
the interface falls back to English while retaining that saved choice; it can
be restored if the same language is installed again. If browser storage is
blocked, the current page can still switch language.

Interface translations are independent of the 18 pinned random-word dictionaries
and their `lang` API/query parameter. All dictionaries remain compiled into
every installer profile. Removing a Russian interface translation, for example,
does not remove Russian random words, change their sampling, or change an
existing phrase, hash or dictionary preference cookie. See
[word languages](word-languages.md) for dictionary provenance and selection.

Catalog checks enforce identical message keys and placeholders across languages.
Browser tests cover translated text, saved choices, direction, narrow viewports
and isolation from sample bytes, statistics, hashes and random words. Additional
tests exercise installed subsets and removing/restoring a language across server
restarts. Translation refinements can be made without changing dictionary data.

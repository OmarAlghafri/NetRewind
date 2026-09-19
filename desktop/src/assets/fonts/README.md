# Vendored fonts

Closes P1-05: `App.css` named Inter, IBM Plex Sans Arabic, and JetBrains
Mono in its `font-family` stacks without shipping any of them, so the app's
actual typography depended entirely on whatever fallback each OS happened
to have installed. These four families are vendored here as WOFF2 and
loaded via `@font-face` in `fonts.css` - no runtime fetch, matching the
CSP's `font-src 'self'`.

All four are **SIL Open Font License 1.1** (`<family>/OFL.txt` in each
directory, fetched from the same `google/fonts` source as the binaries
below - not retyped from memory). License terms are unchanged from
upstream; nothing here modifies the fonts.

## Usage

| Family | Role | Weights vendored |
|---|---|---|
| IBM Plex Sans Arabic | Arabic body text | 400, 500, 600 |
| Alexandria | Arabic headings | 600, 700 |
| Inter | Latin body/UI text | 400, 600, 700 |
| JetBrains Mono | Technical values (IPs, IDs, paths) | 400, 500 |

Only the `latin` and `arabic` Unicode-range subsets were fetched (see
`fonts.css`'s `unicode-range` on each `@font-face`) - this project ships
only Arabic and English (`docs/product/support-matrix.md`), so cyrillic,
greek, vietnamese, etc. subsets would be dead weight. Total vendored size:
**440KB** across 15 files, none of it loaded unless the browser actually
needs that weight/subset (`font-display: swap` plus per-subset
`unicode-range` means, e.g., the Latin engineer never downloads the Arabic
glyphs and vice versa).

## Provenance

Fetched directly from Google Fonts' CDN (`fonts.gstatic.com`) via the same
`css2` API a browser uses to self-host a Google Font - these are the exact
bytes a browser would request, saved locally instead of fetched at runtime.
One request per family+weight (requesting several weights in one combined
`wght@400;600;700` query was tried first and turned out to make Google's
API return three declarations that all pointed at the *same* file,
verified by comparing SHA-256 hashes - a real bug in that shortcut, not a
formatting preference; one request per weight reliably returns distinct
files, verified the same way). Retrieved 2026-09-19.

### Inter (gfonts API v20, upstream: <https://github.com/rsms/inter>)

| File | Weight | Subset | Size | SHA-256 |
|---|---:|---|---:|---|
| `inter/inter-400-latin.woff2` | 400 | latin | 23.1KB | `8909904ab6c872eb994093482a88a28eca2cd95912d7b6fecd72103b0dc07edc` |
| `inter/inter-600-latin.woff2` | 600 | latin | 23.9KB | `f9a06e79cd3a2a20951c0f0e28f66dd0e6d3fda73911d640a2125c8fcb78f21a` |
| `inter/inter-700-latin.woff2` | 700 | latin | 23.8KB | `6f56409fd3d64bb85f7d070bce20749db2d66b6d63cec586cc22d1c761be2491` |

### JetBrains Mono (gfonts API v24, upstream: <https://github.com/JetBrains/JetBrainsMono>)

| File | Weight | Subset | Size | SHA-256 |
|---|---:|---|---:|---|
| `jetbrains-mono/jetbrains-mono-400-latin.woff2` | 400 | latin | 20.7KB | `14425ba9c695763c1547f48a206b7aa60350a33ae23de09f0407877f3fcd89eb` |
| `jetbrains-mono/jetbrains-mono-500-latin.woff2` | 500 | latin | 21.3KB | `cb182feeed4d798ff6961d3c79f7026279448fca0676438aaecb21f3fc39553a` |

### IBM Plex Sans Arabic (gfonts API v15, upstream: <https://github.com/IBM/plex>)

| File | Weight | Subset | Size | SHA-256 |
|---|---:|---|---:|---|
| `ibm-plex-sans-arabic/ibm-plex-sans-arabic-400-arabic.woff2` | 400 | arabic | 41.8KB | `6010e7fd0dce5d527583951750728cbe3c895efbd16aa0f809ab8c824878c9d8` |
| `ibm-plex-sans-arabic/ibm-plex-sans-arabic-400-latin.woff2` | 400 | latin | 18.7KB | `9ed8dcb02e6c7246de4c120295fafa39a7bb73085f4951a4524a07dc911069f2` |
| `ibm-plex-sans-arabic/ibm-plex-sans-arabic-500-arabic.woff2` | 500 | arabic | 44.2KB | `90aef64fea9794f232332e907d45810ab268d1a11721340025eba2a2cdb36d5c` |
| `ibm-plex-sans-arabic/ibm-plex-sans-arabic-500-latin.woff2` | 500 | latin | 19.6KB | `be6a3b2e37f3ad67aa822a55ce356d28c416331c97530a1ec076c2118240ca2d` |
| `ibm-plex-sans-arabic/ibm-plex-sans-arabic-600-arabic.woff2` | 600 | arabic | 44.6KB | `16734a5adb27b0f363e566cbbeacec480da0dc0baa19c8f0053251c2e2bc0eac` |
| `ibm-plex-sans-arabic/ibm-plex-sans-arabic-600-latin.woff2` | 600 | latin | 20.0KB | `63f4757271e403f7baec0862f284ffaa4560ea6096ba9fe8bc6e585ca656e724` |

### Alexandria (gfonts API v6, upstream: <https://github.com/Gue3bara/Alexandria>)

| File | Weight | Subset | Size | SHA-256 |
|---|---:|---|---:|---|
| `alexandria/alexandria-600-arabic.woff2` | 600 | arabic | 12.9KB | `73aaa55f3a3dd4fd524a7c7ee38355c14de74f8fce9de9450856012506a220cf` |
| `alexandria/alexandria-600-latin.woff2` | 600 | latin | 12.9KB | `89da8554698ca09884b53a465bece84c45fac99ce0c220267232a2f69d238bf0` |
| `alexandria/alexandria-700-arabic.woff2` | 700 | arabic | 12.9KB | `dce0b86c40a96ffbeedbe24d2ea6e358749b6590fe1e016a109558e8240c90c7` |
| `alexandria/alexandria-700-latin.woff2` | 700 | latin | 12.9KB | `b9162e96620b03b5da49fa6d5c0b68834942659481d7be10dbff7895b38da3bc` |

`gfonts API vNN` is the version segment Google's CDN URL carries for that
family (e.g. `fonts.gstatic.com/s/inter/v20/...`) - a real, checkable pin
(re-fetching the same family+weight from the API reproduces the same file,
confirmed by hash, for as long as Google serves that API version), though
it is Google's own build counter rather than a claim about which upstream
GitHub tag it corresponds to.

## Re-fetching / updating

There is no repository script for this (a one-off fetch, not a recurring
build step). To refresh a family: request
`https://fonts.googleapis.com/css2?family=<Name>:wght@<weight>&display=swap`
with a modern browser `User-Agent` header (WOFF2 is only served to clients
that report supporting it), one weight per request, take the `latin`/
`arabic` blocks' `url(...)` values, and verify the new file's hash differs
from the one it replaces before trusting it.

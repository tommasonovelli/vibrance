# Vendored files of the web interface

Everything in `web/ui/` is written here, with these exceptions. The interface asks no other host for anything: no CDN, no remote font, no remote image (CLAUDE.md, I12). The files come from Vibrance MusicLib 1.2.0, [`vibrance-musiclib`](https://github.com/tommasonovelli/vibrance-musiclib), whose interface Vibrance takes as its own.

## Hanken Grotesk

| | |
|---|---|
| Files | `fonts/hanken-grotesk-v12-latin.woff2`, `fonts/hanken-grotesk-v12-latin-ext.woff2`, `fonts/OFL.txt` |
| What | [Hanken Grotesk](https://github.com/marcologous/hanken-grotesk), variable weight 100 to 900, version 12, the `latin` and `latin-ext` subsets |
| License | SIL Open Font License 1.1 |
| Source | `web/hanken-grotesk-v12-latin.woff2`, `web/hanken-grotesk-v12-latin-ext.woff2` and `web/OFL.txt` of MusicLib 1.2.0, copied byte for byte |
| `hanken-grotesk-v12-latin.woff2` | 34704 bytes, SHA-256 `e9201eddf1d41d0b62253295d869ce3cf65768f7102b797f02c7f8c876b4a9d5` |
| `hanken-grotesk-v12-latin-ext.woff2` | 19588 bytes, SHA-256 `768af2923e0ab1549f1dfba0a5c8ea749c4c01f01d8e77ffaf7fcd12f57a0a24` |
| `OFL.txt` | 4402 bytes, SHA-256 `e02ccb89a86839b22feff7872ff5cc355cc0f58318d29eee20e2cf83a612f16d` |

The first line of `OFL.txt` is the copyright notice of the font:

```text
Copyright 2021 The Hanken Grotesk Project Authors (https://github.com/marcologous/hanken-grotesk)
```

The notice names the Project Authors as a body and no person. The font is not modified. `licenses/hanken-grotesk/OFL.txt` is a byte copy of `fonts/OFL.txt`; [THIRD_PARTY_NOTICES.md](../../THIRD_PARTY_NOTICES.md) lists the font. `app.css` declares the two files with `@font-face` and `unicode-range`, as MusicLib does, and `index.html` preloads the `latin` one.

## Grain

| | |
|---|---|
| File | `grain.svg` |
| What | The noise texture of the glow at the foot of the sidebar, of the sign-in page and of the colour field behind a head |
| Source | `web/grain.svg` of MusicLib 1.2.0, byte for byte |
| Size | 382 bytes |
| SHA-256 | `82ab564b0c0b9c7feb7a8d841bf031d1f1e0089f037fdaa5958d9b11da27d7e0` |

It is a file and not a `data:` URI because the page is meant to be served with `default-src 'self'`.

## The sun symbol

The `brand-sun` symbol, inline in `index.html` and `login.html`, and `favicon.svg` draw the sun of MusicLib. The path data of the symbol is the one of the `brand-sun` symbol of MusicLib's `web/layout.html`, byte for byte; `favicon.svg` has the path data of MusicLib's `web/favicon.svg` with Vibrance's Magenta (`#d23edd`) as its fill.

The symbol is the owner's mark and is not under the MIT License. MusicLib's `LOGO.md` says: "The sun symbol is original artwork by tommasonovelli. It is not covered by the project's MIT License: all rights reserved." and "You may keep the symbol in unmodified copies of this project, including when you redistribute them. If you distribute a modified version to others, replace the symbol with your own." The same terms are those of this one. That file concerns the symbol only.

The symbol is also in the README banner, [`docs/assets/banner.jpg`](../../docs/assets/banner.jpg), and in the README screenshots, `docs/assets/screenshot-*.jpg`, under the same terms.

## The wordmark

The `brand-word` symbol of `index.html` and `login.html` is the word "Vibrance" as vector outlines, in `--ink`. It was drawn the way MusicLib drew its own wordmark (MusicLib's N-309): the letterforms of Bricolage Grotesque, outlined, with no font file in the repository or in the image.

| | |
|---|---|
| Font | [Bricolage Grotesque](https://github.com/google/fonts/tree/main/ofl/bricolagegrotesque), variable |
| Source | `google/fonts`, commit `6ce172f74aa355ea43eb964fa4a91570a4d3064d` |
| TTF SHA-256 | `413e7357809ddd12fd80a96a8a396de0e401638d4acd3cb3e37532f0472ac682` |
| Instance | `wght` 700, `wdth` 100, `opsz` 20 |
| Tracking | -20 units (-0.02 em) |
| Shaping | HarfBuzz, with `kern` and `liga` |
| Outlines | integer coordinates, the y axis flipped to the SVG's |
| License of the font | SIL Open Font License 1.1 |

Outlines of an OFL font used as the drawing of a logo, with the font itself not distributed, are the same practice MusicLib documents as N-309. The wordmark is shown 20 px to the em, `83.14` by `16.44` px.

The README banner, `docs/assets/banner.jpg`, draws the same outlines in white, 96 px to the cap height, next to the sun, on a magenta gradient with a grain. It is made from these two symbols alone, with no font file.

## `no-cover.svg`

Written here: the quiet disc that stands for a cover that is missing or cannot be loaded. It is not vendored.

# Vendored files of the API documentation page

The page the server answers at `/api/docs` (`index.html`, written here) loads one script, which is vendored: the server carries it in its binary and never asks another host for anything (DESIGN.md §8.8, I12). `web/embed_test.go` fails when the file is not the one recorded here.

| | |
|---|---|
| File | `web/docs/scalar.js` |
| What | [Scalar API Reference](https://github.com/scalar/scalar), the standalone browser build |
| Package | `@scalar/api-reference` (npm) |
| Version | `1.72.4` |
| License | MIT |
| Source | `https://registry.npmjs.org/@scalar/api-reference/-/api-reference-1.72.4.tgz` |
| Integrity of the tarball (npm registry) | `sha512-HKsUqCJbXhmz/5j6ETceXVgnlZKOX1uidF7jz8elSuzKE6FJjJYhK1G7WqxHk4zLabbSb13QHQ9yKUikf8XLBQ==` |
| Path in the tarball | `package/dist/browser/standalone.js` |
| Size | 4381105 bytes |
| SHA-256 | `f5ac0c3504a7ca77ca9fa96a6bb7c8f98b42c271864df35583f6ed9e6da40c23` |

## License

Scalar API Reference is Copyright (c) 2023-present Scalar, under the MIT License. The npm package declares `MIT` but carries no license file, so the text below is the `LICENSE` of Scalar's repository, <https://github.com/scalar/scalar>; it is also `licenses/scalar/LICENSE`, which the image carries in `/usr/share/doc/vibrance/` and [THIRD_PARTY_NOTICES.md](../../THIRD_PARTY_NOTICES.md) lists. The file is a bundle that also contains the libraries Scalar is built with; it keeps their license comments.

```text
MIT License

Copyright (c) 2023-present Scalar

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## The file

The file is the one of the tarball byte for byte: `.gitattributes` keeps Git from converting its line endings. Its last line names a source map, `standalone.js.map`, which is not vendored: a browser with its developer tools open asks for `/api/docs/standalone.js.map` and gets `404`.

## To change the version

1. Pick an exact version, never `latest`. Read `dist.integrity` in `https://registry.npmjs.org/@scalar/api-reference/<version>`.
2. Download the tarball of that version and check it: `openssl dgst -sha512 -binary api-reference-<version>.tgz | base64 -w0` must print the integrity value without its `sha512-` prefix.
3. Copy `package/dist/browser/standalone.js` over `web/docs/scalar.js` and write the new version, integrity, size and `sha256sum` in the table above.
4. Open `/api/docs` in a browser with the console open, as the next section says: the policy of the page is as narrow as this version needs, and another version may need another.

## What the page allows the script

The page has its own `Content-Security-Policy` (`DocsCSP` in `internal/api/docs.go`): the script of this server, inline styles, and requests to this server. Nothing else: no other host, no inline script, no `eval`, no image, no font, no frame, no worker. The configuration in `index.html` turns off the parts of the script that would ask another host (its web fonts, its telemetry, its assistant, its links to the hosted client); the policy refuses them in any case.

Version 1.72.4 was tried with this policy in Chromium: the page, every section of the specification, the search, the download of the document and a request sent from the page ("Test Request") work. The console shows one refusal, of an `eval` the script tries once and does not need.

To try a request other than GET from the page, add the header `X-Vibrance-Request: 1` in the "Headers" table of its request client: the server refuses such a request without it (`403 request_header_required`).

# ADR 0005: Swagger UI lokal dan Postman Collection hasil generate

- Status: Accepted
- Tanggal: 2026-11-17
- Tiket: DX-001 (`SYSTEM_DESIGN.md` 5.6)

## Konteks

Dokumentasi HTTP API harus selalu sinkron dengan implementasi. `api/openapi/openapi.yaml` (OpenAPI 3.1) adalah sumber tunggal. Swagger UI dan Postman Collection harus diturunkan darinya, bukan ditulis ulang, dan tidak boleh menambah service atau membuka akses baru di production.

## Keputusan

1. **Swagger UI di aplikasi yang sama.** `GET /swagger`, `GET /swagger/assets/{file}`, dan `GET /openapi.yaml` didaftarkan oleh Goravel/Fiber hanya jika `DOCS_ENABLED=true`. Startup menolak `DOCS_ENABLED=true` bila `APP_ENV` bukan `local` atau `testing`. Compose dev/agent mengaktifkannya; image release tidak membawa `public/swagger` maupun `api/` (`.dockerignore`).
2. **Aset lokal dan dipin.** `swagger-ui-dist` 5.35.0 (versi 5.x terbaru saat dipin; mendukung OpenAPI 3.1) di-vendor ke `public/swagger/vendor` oleh `scripts/update-swagger-ui.sh`, yang **membandingkan** sha512 tarball dengan nilai tercatat (`sha512-U06rpMtW9V3XPGOBeamTUSr0Z09jMX4IJuANpE/EZbAhw0G8CgwrzU4YmXkn/h0wCDNsXdGarTNNHC3sU8yVUQ==`) dan berhenti bila berbeda. Hanya `public/swagger` yang di-mount. Tidak ada CDN, sehingga berjalan offline di Docker.
3. **Hardening halaman docs.** CSP `script-src 'self'` (inisialisasi di `swagger-initializer.js`, tanpa inline script), `frame-ancestors 'none'`, `nosniff`, `no-referrer`. Swagger UI tidak menerima `?url=` (`queryConfigEnabled: false`) dan tidak menyimpan token (`persistAuthorization: false`). Hanya tiga file aset yang ada di whitelist; path request tidak pernah dipakai sebagai path file.
4. **Postman dari generator tunggal, rantai pasok dikunci.** `scripts/generate-postman.sh` memasang `openapi-to-postmanv2` 6.3.3, `js-yaml` 4.1.0, dan `newman` 6.2.3 dengan `npm ci` dari `scripts/postman/package-lock.json` ke volume Docker (satu-satunya langkah dengan jaringan), memakai image `node:22-alpine` yang dipin dengan digest. Konversi berjalan di container kedua dengan `--network none`, root filesystem read-only, spec dan tool read-only, dan hanya `api/postman` yang bisa ditulis; repository (termasuk `.env.agent`) tidak pernah di-mount. Lalu `scripts/postman/normalize.js` membuat output deterministik: id acak dihapus, `{{baseUrl}}`, `{{accessToken}}`, `{{productCode}}`, `{{variableCode}}`, auth eksplisit per request (`noauth` untuk `security: []`, selain itu bearer), parameter query opsional nonaktif. `--check` meregenerasi dan gagal bila berbeda dari file yang di-commit.
5. **Gate parity di Go test.** `tests/feature/api_contract_test.go` membandingkan route Goravel yang terdaftar, operasi OpenAPI, dan request Postman sebagai himpunan `(METHOD, path)`, memeriksa `operationId` unik, auth sesuai `security`, format v2.1, dan jumlah operasi (8 pada baseline M1). `tests/feature/api_behaviour_test.go` menambah nama parameter spec vs koleksi, validitas `x-required-permission`, dan bukti **dari kode yang berjalan**: operasi ber-`security` menolak anonim, dan setiap role demo mendapat 403 tepat bila matriks permission tidak memberinya akses. Route tooling docs dan route `/test/*` dikecualikan secara eksplisit.
6. **Smoke import.** `scripts/smoke-postman.sh` menjalankan koleksi dengan Newman di network Compose (`http://api:3000`) memakai token read-only `demo_auditor` (analyst boleh menulis dictionary sejak RF-005): baca 200, tulis 403, login contoh 401 (password placeholder), tanpa mengubah data.

## Alternatif

- **Service Swagger terpisah (`swaggerapi/swagger-ui`):** menambah container, dilarang oleh 5.6.
- **Aset dari CDN:** tidak berjalan offline dan membuka dependensi pihak ketiga saat runtime.
- **Collection ditulis manual:** pasti drift dari spec.

## Konsekuensi

- Setiap perubahan endpoint wajib: perbarui spec, `redocly lint`, `./scripts/generate-postman.sh`, commit hasilnya, dan jalankan test. Parity test gagal bila salah satunya terlewat.
- Contoh di spec harus deterministik. Schema tanpa `example` membuat konverter mengarang nilai acak, sehingga `--check` gagal.
- Menaikkan versi Swagger UI atau konverter berarti menjalankan ulang skrip terkait, meninjau diff, dan memperbarui ADR ini. Untuk konverter: ubah `scripts/postman/package.json`, regenerasi lockfile, lalu `--check`. Untuk Swagger UI: set `SWAGGER_UI_VERSION` dan `SWAGGER_UI_INTEGRITY` (dari `npm view swagger-ui-dist@<v> dist.integrity`).
- Parity test tidak membandingkan schema request/response body dengan kode; kesesuaian body dijaga oleh test HTTP per endpoint dan review.

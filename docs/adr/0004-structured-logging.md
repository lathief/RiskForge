# ADR 0004: Structured logging per tipe dan per modul

- Status: Accepted
- Tanggal: 2026-11-17
- Tiket: LOG-001

## Konteks

Tim membutuhkan log yang bisa dibaca mesin dan dikorelasikan, mengikuti format log gateway yang sudah dipakai (`app_name`, `clientapp`, `path_alias`, `requestID`, `requestFrom`, `requestUser`, `deviceID`, `requestTags`, `requestBody`, `responseBody`, `responseTime`, `httpCode`, `memoryUsage`, `requestAt`), dipisah menjadi empat file: aktivitas HTTP, error, siklus hidup proses, dan query database. `SYSTEM_DESIGN.md` 15.1 dan 15.3 mewajibkan log tanpa credential dan PII.

## Keputusan

1. **Empat file JSON-lines** di `storage/logs`: `activity.log`, `error.log`, `lifetime.log`, `query.log`. Ditulis oleh `app/platform/logging` (standard library saja). Log framework Goravel (`goravel-*.log`) tetap ada untuk pesan framework.
2. **Korelasi:** setiap entri memuat `type`, `module`, dan `requestID`. `module` diisi oleh middleware `Module` per grup route (`identity`, `catalog`; `platform` untuk health, 404, startup) dan oleh `database.Manager.WithModule(...)` per repository.
3. **Redaksi wajib, bukan opsional.** Body request/response dicatat setelah key sensitif (`password`, `*token*` kecuali `token_type`, `secret`, `api_key`, `authorization`, `cookie`, `credential`, `pin`, `otp`, dll.) diganti `[REDACTED]` di kedalaman mana pun. Header `Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key` juga disensor. Body yang bukan JSON valid atau lebih dari `LOG_MAX_BODY_BYTES` hanya dicatat ukurannya, karena tidak bisa disensor dengan andal.
4. **Penyesuaian dari template gateway:**
   - `clientip` berisi IP klien, bukan `${referer}`; referer dicatat di field `referer`.
   - `httpCode` berisi status sebenarnya, bukan konstanta `200`.
   - `responseTime` dalam milidetik (angka), `memoryUsage` adalah byte heap hidup dari `runtime/metrics` (tanpa stop-the-world).
   - `requestUser` adalah header dari klien dan tidak tepercaya; `userID` ditambahkan dari token yang sudah diverifikasi.
   - `requestHeaders` dan `responseHeaders` ditambahkan sesuai permintaan.
5. **query.log** mencatat SQL (spasi dipadatkan, maks 2000 karakter), jumlah argumen, jumlah baris, durasi, `slow` (≥ `LOG_SLOW_QUERY_MS`), dan hasil transaksi (`COMMIT`/`ROLLBACK` beserta durasi total). **Nilai argumen tidak pernah dicatat** karena bisa berisi hash password atau data pribadi.
6. **lifetime.log:** `starting`, `signal_received`, `stopped`, `startup_failed`, `crashed`. Agar `stopped` tercatat, `compose.yaml` menjalankan binary hasil build dengan `exec`, karena `go run` tidak meneruskan SIGTERM.
7. **error.log:** panic (dengan stack trace, tanpa header/body request) dan error/warning yang ditangani controller atau middleware.

## Alternatif

- **Channel Goravel (`facades.Log()`) per file:** format barisnya teks dengan prefiks, bukan JSON murni, dan tidak memberi kontrol nama field.
- **`log/slog`:** menambahkan `time`/`level`/`msg` dengan nama tetap; struct dengan tag JSON memberi urutan dan nama field persis seperti template.
- **Middleware logger Fiber dengan template string:** template menyisipkan body mentah (`${body}`, `${resBody}`) tanpa redaksi dan bergantung langsung pada Fiber, yang dilarang di luar driver.

## Konsekuensi

- Belum ada rotasi file. Karena itu image release tidak menulis file sama sekali (`LOG_FILES=false`) dan hanya mencetak ke stdout (`LOG_PRINT=true`), sehingga rotasi ditangani runtime container. File di `storage/logs` hanya ada di dev/test. Rotasi/pengiriman log ke sistem terpusat masuk RF-023.
- Query lewat query builder `Table()` belum dicatat; repository proyek ini memakai SQL mentah.
- `413` ditolak fasthttp sebelum middleware berjalan, sehingga tidak tercatat di `activity.log`. `408` **tercatat**: middleware timeout driver berada di luar `ActivityLog`, handler tetap berjalan sampai selesai, dan `ActivityLog` mencatat `408` (bukan status handler) begitu deadline lewat. Panic setelah deadline tidak lagi diteruskan driver ke `Recover`, jadi `ActivityLog` sendiri yang menulisnya ke `error.log`. (Koreksi review kedua; versi awal ADR ini keliru menyatakan 408 tidak tercatat.)
- Middleware timeout Fiber v3 menjalankan handler di goroutine sendiri, menjawab 408 tepat saat deadline, lalu dapat memakai ulang context request untuk request lain sementara handler masih berjalan. Karena itu `ActivityLog` menyalin semua data request **sebelum** handler berjalan dan tidak menyentuh request/response lagi setelah deadline. Handler yang tetap menulis respons setelah deadline memicu data race di dalam driver (ditemukan oleh `-race`); aturannya: handler wajib menghormati `ctx.Context()` (query database sudah memakainya) dan tidak menulis respons setelah context selesai.
- Image release memakai `LOG_HTTP_BODIES=false`: redaksi berbasis nama key tidak mengenali PII seperti NIK atau alamat, jadi body hanya dicatat di dev/test dengan data sintetis.
- `pin`, `otp`, `auth` dicocokkan persis (bukan substring) agar `shipping` atau `author` tidak ikut disensor; `Referer` dicatat tanpa query string dan fragment.

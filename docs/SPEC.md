# DesaKita — Spesifikasi MVP (Tahap 1)

Status: Bagian 1–4 disetujui. Bagian 5–6 adalah **usulan** yang belum ditinjau, tetapi sudah diimplementasikan. Keputusan yang diambil selama implementasi dicatat di §9.

## 1. Tujuan dan konteks

DesaKita adalah website panduan dan pemesanan desa wisata. Masalah yang dijawab:

- Desa wisata kecil sulit ditemukan wisatawan.
- Promosi bergantung pada media sosial yang tidak teratur.
- Pemesanan dilakukan lewat chat tanpa catatan.
- Wisatawan sulit membandingkan paket.

Setiap desa punya profil, paket, kalender ketersediaan, dan permintaan pemesanan. Semuanya diatur pengelola lewat dashboard sederhana. Proyek ini selaras dengan SDG 8, 11, dan 12.

**Arah proyek:** dimulai sebagai portofolio, tapi dibangun dengan kualitas siap pakai (migrasi rapi, konfigurasi lewat env, Docker). MVP diisi data contoh. Domain, email produksi, dan backup ditunda.

**Pengguna:**
- **Wisatawan:** mencari, membandingkan, dan memesan.
- **Pengelola desa:** mengisi profil, membuat paket, mengatur kalender, dan menanggapi permintaan.
- **Admin:** memverifikasi desa dan menangani laporan.

Katalog bisa dilihat tanpa login. Login diperlukan untuk memesan atau melapor.

### Cakupan MVP

- Register dan login.
- Profil desa: deskripsi, foto, lokasi, kontak.
- Paket: nama, deskripsi, harga per orang, durasi, kuota.
- Kalender ketersediaan.
- Permintaan pemesanan.
- Pencarian dengan filter daerah, jenis kegiatan, dan harga.
- Dashboard pengelola.
- Verifikasi desa dan penanganan laporan oleh admin.
- Hapus akun sendiri.

### Di luar cakupan (tahap berikutnya, masing-masing dengan spec sendiri)

- **Tahap 2:** ulasan (hanya untuk pemesanan `completed`) dan moderasinya, peta Leaflet/OSM, notifikasi email, oleh-oleh UMKM, antarmuka bahasa Inggris.
- **Tahap 3:** pembayaran uang muka, statistik pengelola, estimasi jejak karbon, PWA, laporan dampak desa.
- **Pembayaran online sengaja ditunda.** Pembayaran diatur langsung antara wisatawan dan pengelola.

### Keputusan terbuka

- **Wilayah awal: belum diputuskan.** Tabel `regions` dibuat generik dan migrasi tidak berisi data wilayah. Seed pengembangan hanya memuat wilayah contoh.
- Desa mitra nyata untuk uji coba di minggu ke-8.

## 2. Teknologi dan struktur

- **Backend:** Go dengan router Chi.
- **Data:** PostgreSQL (target kompatibel ≥ 14), sqlc (pgx/v5), dan golang-migrate.
- **Tampilan:** Go `html/template`, HTMX, dan Tailwind.
- **Bahasa:** semua teks UI lewat lapisan terjemahan. MVP hanya memiliki `id`.
- **Kode:** kode, nama tabel, dan komentar dalam bahasa Inggris. Dokumen dalam Bahasa Indonesia.
- **Auth:** session cookie dengan password di-hash argon2id.
- **Penyimpanan foto:** disk lokal, lalu pindah ke object storage S3-compatible di tahap selanjutnya.
- **Deployment:** Docker.
- **Zona waktu:** aplikasi memakai `Asia/Jakarta`. Uang disimpan sebagai `bigint` rupiah. Semua kolom waktu bertipe `timestamptz`, dan tanggal kunjungan bertipe `date`.

### Struktur: modul per domain

```
cmd/desakita/          titik masuk (serve, migrate, seed, promote-admin)
internal/
  auth/                register, login, session, hapus akun
  village/             profil desa, keanggotaan, foto desa
  tourpackage/         paket wisata, jenis kegiatan, foto paket
  booking/             kalender, kuota, pemesanan, kedaluwarsa + tes konkurensi
  admin/               verifikasi desa, laporan
  platform/            config, database, session, csrf, ratelimit, upload, i18n, middleware
  db/                  kode hasil sqlc
migrations/  queries/  seeds/  web/templates/  web/static/
```

Setiap modul punya lapisan handler → service → store.

**Aturan dependensi (satu arah):**

```
platform ← auth ← village ← tourpackage
platform ← booking
village ← admin
```

- `booking/` tidak mengimpor `tourpackage/` maupun `village/`. Modul ini mendefinisikan interface kecilnya sendiri, `PackageLookup` dan `VillageAccess`, dan `internal/app` yang merangkai adapternya.
- Semua logika kuota dan tesnya berada di `booking/`.

## 3. Model data

| Tabel | Kolom utama | Catatan |
|---|---|---|
| `users` | id, email `citext` unik, password_hash, name, phone?, is_admin, created_at | — |
| `sessions` | token, data, expiry | penyimpanan session |
| `regions` | id, parent_id?, kind (`province`/`regency`/`district`), name | generik, tanpa data di migrasi |
| `activity_types` | id, slug, name | — |
| `villages` | id, slug, name, description, district_id → regions, address, lat?, lng?, contact_phone, contact_whatsapp?, status (`pending`/`verified`/`rejected`/`suspended`), review_note, verified_by?, verified_at? | — |
| `village_members` | (village_id, user_id), role (`owner`) | tidak ada kolom role "pengelola" di users |
| `tour_packages` | id, village_id, name, description, price_per_person_idr, duration_days ≥ 1, duration_nights ≥ 0, default_quota > 0, is_active | — |
| `package_activity_types` | (package_id, activity_type_id) | — |
| `media` | id, village_id?, package_id?, path, mime, bytes, sort_order, consent_confirmed, uploaded_by? | **tepat satu target**: `num_nonnulls(village_id, package_id) = 1` |
| `availability` | (package_id, date), quota, reserved, is_closed | `CHECK (0 ≤ reserved ≤ quota)`, milik `booking/` |
| `bookings` | id, (package_id, village_id) → tour_packages, traveler_id?, start_date, party_size, note, total_price_idr (snapshot), contact_name, contact_phone, status, expires_at, decided_at?, completed_at?, cancelled_at?, cancelled_by? (`traveler`/`manager`), decision_note | — |
| `reports` | id, reporter_id?, target_type (`village`/`package`), target_id, reason, status (`open`/`resolved`/`dismissed`), resolved_by?, resolved_at? | — |

Tabel ulasan dibuat di tahap 2.

### Aturan model

- **Peran:** pengelola adalah pengguna yang punya baris di `village_members`. Satu akun bisa menjadi wisatawan sekaligus pengelola.
- **Kuota paket multi-hari dihitung per tanggal keberangkatan saja.** Paket 2H1M yang berangkat tanggal 10 hanya memotong kuota tanggal 10. Pengelola menutup tanggal yang bentrok secara manual.
- **Baris `availability` dibuat secara lazy.** Tanggal dalam jendela pemesanan otomatis terbuka dengan kuota `default_quota`. Baris baru dibuat saat pengelola mengubah tanggal itu atau saat ada pemesanan pertama.
- **Kuota tidak boleh diturunkan di bawah `reserved`.** Aplikasi menolak dengan pesan yang jelas, dan constraint `CHECK` menjadi pengaman kedua.
- **Menutup tanggal hanya menghentikan pemesanan baru.** Pemesanan yang sudah ada tidak ikut dibatalkan.
- **Hapus akun:**
  - Ditolak jika pengguna adalah pengelola terakhir sebuah desa.
  - Jika berhasil, dalam satu transaksi: pemesanan miliknya mendapat `traveler_id = NULL`, `note = ''`, `contact_name = 'Akun dihapus'`, dan `contact_phone = ''`. `reports.reporter_id` di-set NULL. Keanggotaan desa ikut terhapus.
  - Riwayat pemesanan tetap tersimpan sebagai catatan desa.

## 4. Pemesanan

### Status dan transisi yang sah

| Dari → Ke | Oleh | Syarat | Kuota |
|---|---|---|---|
| — → `pending` | wisatawan | lolos validasi | ditahan |
| `pending` → `confirmed` | pengelola desa itu | `now < expires_at` | tetap |
| `pending` → `rejected` | pengelola desa itu | — | dikembalikan |
| `pending` → `expired` | sistem | `now ≥ expires_at` | dikembalikan |
| `pending` atau `confirmed` → `cancelled` | wisatawan pemilik atau pengelola desa itu | `start_date ≥ hari ini` | dikembalikan |
| `confirmed` → `completed` | pengelola desa itu | `start_date ≤ hari ini` | tetap |

- `rejected`, `expired`, `cancelled`, dan `completed` adalah status akhir.
- Setiap transisi berupa satu `UPDATE … WHERE id = $1 AND status IN (…) RETURNING`. Jika hasilnya 0 baris, transisi ditolak dengan `ErrInvalidTransition`. Pengembalian kuota terjadi di transaksi yang sama, sehingga kuota tidak pernah dikembalikan dua kali.

### Validasi saat membuat permintaan

Semua pemeriksaan ini dilakukan di `booking/` dalam satu transaksi. Pemesanan ditolak jika:

- desa belum `verified`
- paket tidak aktif
- `start_date` di luar rentang **lusa s.d. hari ini + 90** (pemesanan paling cepat lusa, agar pengelola punya jendela respons sekitar 24 jam)
- tanggal ditutup
- `party_size < 1`
- kuota tidak cukup
- pengguna sudah punya **3 permintaan `pending`**

Batas 3 `pending` dihitung dalam transaksi yang sama setelah `pg_advisory_xact_lock` per pengguna.

**Kedaluwarsa:** `expires_at = LEAST(created_at + 48 jam, awal hari start_date WIB)`. Nilai 48 jam bisa diatur lewat konfigurasi. Job di dalam aplikasi berjalan setiap 5 menit dan memakai `FOR UPDATE SKIP LOCKED`.

### Mekanisme kuota (satu transaksi)

1. `INSERT INTO availability … ON CONFLICT DO NOTHING` dengan nilai awal `quota = default_quota`.
2. `UPDATE availability SET reserved = reserved + $n WHERE package_id = $1 AND date = $2 AND NOT is_closed AND reserved + $n <= quota RETURNING …`. Jika hasilnya 0 baris, pemesanan gagal dengan `ErrSoldOut`.
3. `INSERT INTO bookings …`.

### Otorisasi

- Setiap aksi memeriksa **kepemilikan pemesanan**, bukan hanya keanggotaan desa.
- Wisatawan hanya bisa mengakses pemesanan dengan `traveler_id` miliknya.
- Pengelola hanya bisa mengakses pemesanan dengan `village_id` desa yang ia kelola.
- Akses yang tidak sah dijawab dengan 404.

### Alur wisatawan

1. Wisatawan membuka halaman paket. Kalender bulanan (HTMX) menampilkan sisa kuota.
2. Wisatawan memilih tanggal, lalu mengisi jumlah orang, catatan, nama, dan no. HP. Data kontak terisi otomatis dari profil.
3. Wisatawan menekan "Kirim permintaan". Jika belum login, ia diarahkan ke login lalu kembali ke halaman paket.
4. Status permintaan bisa dilihat di "Pemesanan saya", termasuk tombol batal.

### Dashboard pengelola (`/kelola`)

Dashboard dirancang mobile-first, dengan tombol besar dan bahasa sederhana.

- **Spanduk status:** muncul saat desa belum diverifikasi. Profil dan paket tetap bisa diisi tetapi belum tampil publik.
- **Beranda** berisi tiga daftar:
  - Perlu ditanggapi: permintaan `pending`, diurutkan menurut `expires_at`, dengan hitung mundur.
  - Kunjungan mendatang.
  - Perlu ditandai selesai.
- **Detail permintaan:** hanya tombol untuk transisi yang sah yang ditampilkan.
- **Kabari via WhatsApp:**
  - Tombol ini membuat link `https://wa.me/<nomor>?text=<pesan>`.
  - Nomor dinormalisasi ke format `62…`: semua non-digit dibuang, awalan `0` diganti `62`, dan awalan `+62`/`62` dipertahankan. Nomor valid jika panjangnya 10–15 digit.
  - Pesan di-URL-encode.
- **Paket:** daftar, buat atau ubah, aktif/nonaktif, dan foto.
- **Kalender per paket:**
  - Setiap tanggal menampilkan `reserved/quota`.
  - Pengelola bisa menutup atau membuka tanggal dan mengubah kuota secara inline.
- **Profil desa:** setiap foto wajib dicentang "persetujuan warga/pengelola".

## 5. Admin (usulan)

- **Admin pertama** dibuat lewat CLI: `desakita promote-admin <email>`. Tidak ada form web untuk menjadi admin.
- **Antrean verifikasi:** admin melihat desa `pending` beserta profil dan kontaknya, lalu memilih **Verifikasi** atau **Tolak** (alasan wajib, disimpan di `review_note`).
- **Suspend desa:** desa `verified` bisa di-suspend. Akibatnya desa hilang dari katalog dan permintaan baru ditolak. Pemesanan yang ada tidak dibatalkan otomatis, dan admin menghubungi pengelola secara manual.
- **Laporan:** pengguna yang login bisa melaporkan desa atau paket. Admin melihat laporan `open` dan menandainya `resolved` atau `dismissed`. Setiap aksi admin mencatat pelaku dan waktunya (`verified_by`, `resolved_by`, dan seterusnya).

## 6. Keamanan, penanganan galat, dan pengujian (usulan)

### Keamanan

- **Password:**
  - Di-hash dengan argon2id. Panjang minimal 8 karakter.
  - Login selalu menjalankan verifikasi hash agar waktunya tidak membocorkan email mana yang terdaftar.
- **Session cookie:**
  - `HttpOnly` dan `SameSite=Lax`. Atribut `Secure` dipakai di produksi.
  - Token diperbarui saat login dan logout.
  - Lifetime 30 hari dan idle timeout 7 hari.
- **CSRF:** semua request yang mengubah data melewati middleware CSRF berbasis token (form tersembunyi + header `X-CSRF-Token` untuk HTMX), dilengkapi `SameSite=Lax`.
- **Redirect setelah login** hanya menerima path internal. Nilainya harus diawali `/`, tidak boleh diawali `//` atau `/\`, dan tidak boleh mengandung skema atau host. Selain itu, pengguna diarahkan ke `/`.
- **Rate limit:** in-memory per IP dan per akun, cukup untuk satu instance:
  - login (5/menit per IP+email)
  - register
  - kirim permintaan pemesanan
  - laporan
- **Unggah foto:**
  - Ukuran maksimal 5 MB (`http.MaxBytesReader`).
  - Tipe dideteksi dari isi file (`http.DetectContentType`) lalu didekode. Hanya JPEG, PNG, dan WebP yang diterima. Dimensi maksimal 6000×6000 untuk mencegah *decompression bomb*.
  - Gambar di-re-encode ke JPEG agar metadata EXIF, termasuk lokasi GPS warga, terhapus.
  - Nama file berupa string acak, disimpan di luar web root, dan disajikan dengan `X-Content-Type-Options: nosniff`.
- **Validasi input:** semua input divalidasi di server, termasuk batas panjang. Template memakai auto-escape `html/template`.
- **Header keamanan:** CSP `default-src 'self'` (HTMX disajikan lokal), `frame-ancestors 'none'`, `Referrer-Policy: strict-origin-when-cross-origin`, dan `nosniff`.
- **Log:** format JSON (`slog`). Password, catatan pemesanan, dan nomor HP tidak pernah ditulis ke log.
- **Hapus akun** memerlukan konfirmasi password.

### Penanganan galat

- **Galat domain per modul:** `ErrNotFound`, `ErrSoldOut`, `ErrInvalidTransition`, `ErrPendingLimit`, `ErrDateOutOfWindow`, `ErrQuotaBelowReserved`, dan `ValidationError` (berisi peta field → pesan).
- **Pemetaan galat ke HTTP oleh handler:**

  | Galat | Respons |
  |---|---|
  | `ValidationError` | 422, form ditampilkan ulang beserta pesan |
  | `ErrNotFound` dan akses tidak sah | 404 |
  | `ErrSoldOut`, `ErrInvalidTransition` | 409 dengan pesan ramah |
  | rate limit | 429 |
  | galat lain | 500, halaman generik dengan request ID; detail hanya di log |

- **Request HTMX** menerima fragmen galat, bukan halaman penuh.
- **Middleware `recover`** menangkap panic.
- **Transaksi:** semua transaksi memakai isolasi READ COMMITTED dengan `UPDATE` bersyarat. Tidak perlu retry serialisasi.

### Pengujian

- **Unit:**
  - transisi status (berbasis tabel)
  - validasi jendela tanggal (lusa s.d. +90)
  - perhitungan `expires_at`
  - normalisasi nomor WhatsApp dan URL-encode pesan
  - sanitasi redirect
  - deteksi tipe file unggahan
- **Integrasi dengan PostgreSQL asli (`TEST_DATABASE_URL`):**
  - Setiap paket tes memakai database baru yang di-clone dari template hasil migrasi.
  - Mencakup tes migrasi naik dan turun, serta tes constraint (media tepat satu target, `reserved ≤ quota`, email case-insensitive).
- **Tes konkurensi wajib (`booking/`):**
  1. N goroutine memesan kuota terakhir bersamaan. Tepat satu yang berhasil, dan `reserved` tidak pernah melebihi `quota`.
  2. Penolakan bersamaan dengan kedaluwarsa. Tepat satu transisi yang berhasil, dan kuota dikembalikan satu kali.
  3. Pembatalan setelah konfirmasi mengembalikan kuota.
  4. Satu pengguna mengirim banyak permintaan bersamaan. Maksimal 3 yang berstatus `pending`.
  5. Menutup tanggal tidak mengubah pemesanan yang ada.
  6. Menurunkan kuota di bawah `reserved` ditolak.
- **Tes akses:**
  - Lintas desa: pengelola desa A tidak bisa membaca atau mengubah pemesanan desa B.
  - Lintas pengguna: wisatawan X tidak bisa membaca atau membatalkan pemesanan wisatawan Y.
- **Tes HTTP (`httptest`):**
  - Request tanpa token CSRF ditolak.
  - Redirect eksternal setelah login ditolak.
  - Halaman yang membutuhkan login mengarahkan ke `/masuk`.
- **Perintah tes:** `go test -race ./...` dijalankan sebelum setiap commit tahap.

## 7. Urutan implementasi

Setiap tahap di-commit setelah tesnya lulus.

1. Kerangka proyek, migrasi, seed generik, docker-compose, dan `.env` (masuk `.gitignore`). **Berhenti untuk ditinjau.**
2. Auth, session, dan CSRF.
3. Desa, paket, dan unggah foto.
4. Inti pemesanan di `booking/` beserta tes konkurensi, tanpa UI. **Berhenti untuk ditinjau.**
5. Dashboard pengelola dan kalender.
6. Pencarian, admin, dan pengerasan keamanan.

## 8. Risiko

- **Pengelola belum terbiasa dengan teknologi:** dashboard dibuat sangat sederhana, disertai panduan bergambar dan kontak bantuan.
- **Konten awal kosong:** mulai dari 2–3 desa mitra.
- **Penipuan:** ditangani lewat verifikasi manual dan laporan pengguna.
- **Beban admin:** dijaga dengan membatasi cakupan ke satu wilayah.
- **Kuota bisa diborong:** dicegah dengan batas 3 `pending`, rate limit, dan kedaluwarsa 48 jam.

## 9. Catatan implementasi (untuk ditinjau)

Keputusan ini diambil selama implementasi tahap 1–6, karena spec belum mengaturnya.

- **Paket:** tidak bisa dihapus, hanya dinonaktifkan, karena paket bisa sudah punya pemesanan.
- **Slug desa:** tidak berubah walaupun nama desa diganti, supaya link yang sudah dibagikan tetap berfungsi.
- **Foto:**
  - Maksimal 12 foto per desa atau per paket.
  - Diperkecil ke sisi terpanjang 1600 px.
  - Rotasi diambil dari EXIF orientation sebelum EXIF dihapus.
- **Tanggal yang ditutup:** permintaan `pending` di tanggal itu tetap bisa dikonfirmasi.
- **Kedaluwarsa:**
  - Penolakan tetap diizinkan setelah `expires_at` selama job kedaluwarsa belum memprosesnya. Kuota tetap dikembalikan tepat satu kali.
  - Konfirmasi ditolak begitu `expires_at` lewat.
- **Kalender pengelola:** kuota dan buka/tutup hanya bisa diubah untuk rentang hari ini s.d. +90 hari.
- **Pembatalan:** tombol pengelola untuk permintaan `pending` adalah "Tolak". Tombol "Batalkan" untuk pengelola hanya muncul pada pemesanan `confirmed`.
- **Nomor WhatsApp:** nomor yang diawali `8` (tanpa `0`) juga dinormalisasi ke `62…`.
- **Rate limit** (in-memory, per instance):

  | Aksi | Batas |
  |---|---|
  | login | 5/menit per IP+email dan 30/menit per IP |
  | daftar | 20/jam per IP |
  | kirim pemesanan | 10/jam per akun |
  | laporan | 5/jam per akun |

- **IP klien:** `X-Forwarded-For`/`X-Real-IP` hanya dipercaya jika `TRUST_PROXY=true`.
- **Admin:** suspend tidak membatalkan pemesanan yang ada. Desa yang ditolak bisa langsung diverifikasi jika datanya sudah diperbaiki.
- **Pencarian:** pencarian teks sederhana (`ILIKE` pada nama paket dan nama desa) ditambahkan di samping filter daerah, jenis kegiatan, dan harga.

# DesaKita — Spesifikasi MVP (Tahap 1)

Status: Bagian 1–4 disetujui. Bagian 5–6 adalah **usulan** yang belum ditinjau, tetapi sudah diimplementasikan. Keputusan yang diambil selama implementasi dicatat di §9. User story dan kriteria penerimaan, beserta tes yang membuktikannya, ada di §10.

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
- **Laporan:** pengguna yang login bisa melaporkan desa atau paket. Admin melihat laporan `open` dan menandainya `resolved` atau `dismissed`, dengan catatan penyelesaian opsional. Laporan yang sudah ditutup bisa dilihat di tab "Selesai" dan "Diabaikan".
- **Dashboard admin** (`/admin`) berisi:
  - kartu angka (menunggu verifikasi, laporan terbuka, desa dinonaktifkan, desa terverifikasi),
  - antrean verifikasi dengan lama menunggu,
  - aktivitas terbaru.
- **Log audit** (`/admin/audit`, tahap 2):
  - **Aksi yang dicatat:** verifikasi, tolak, nonaktifkan, aktifkan kembali, penyelesaian dan pengabaian laporan, serta pengangkatan admin lewat CLI.
  - **Isi setiap entri:** pelaku (ID dan nama saat itu), aksi, target, status asal dan tujuan, catatan, dan waktu.
  - **Atomik:** entri ditulis dalam transaksi yang sama dengan perubahannya, sehingga perubahan yang gagal tidak tercatat.
  - **Append-only:** tabel `audit_log` dilindungi trigger yang menolak `UPDATE`, `DELETE`, dan `TRUNCATE`. Entri tetap ada walaupun akun admin dihapus.
  - **Tampilan:** halaman audit punya filter aksi dan jenis objek serta pagination 50 entri. Detail desa menampilkan riwayat tindakannya sendiri.

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

## 10. User story dan kriteria penerimaan

Bagian ini menjabarkan cakupan MVP (§1) menjadi cerita per peran. Setiap kriteria bisa dicek satu per satu.

- **Format cerita:** *Sebagai …, saya ingin …, agar …*
- **Bukti:** nama tes otomatis yang memeriksa kriteria itu. Semua tes ada di `internal/` dan dijalankan dengan `go test ./...`.
- **Manual:** belum ada tes otomatis. Kriteria ini dicek dengan mencoba langsung di browser.
- **Status:** semua cerita di bawah sudah diimplementasikan, kecuali yang ditandai *belum*.

| Kode | Peran | Cerita |
|---|---|---|
| W-01 … W-09 | Wisatawan | Menjelajah, mencari, memesan, memantau, membatalkan, melapor, menghapus akun |
| P-01 … P-08 | Pengelola desa | Mendaftarkan desa, mengisi profil dan paket, mengatur kalender, menanggapi permintaan |
| A-01 … A-05 | Admin | Menjadi admin, memverifikasi desa, menonaktifkan desa, menangani laporan |
| S-01 … S-04 | Sistem | Kedaluwarsa otomatis, kuota tidak pernah terjual ganda, keamanan dasar, data terjaga |

### 10.1 Wisatawan

#### W-01 Menjelajah katalog tanpa login

*Sebagai calon wisatawan, saya ingin melihat desa dan paket tanpa membuat akun, agar saya bisa menilai dulu sebelum mendaftar.*

1. Beranda (`/`) menampilkan paket aktif dari desa berstatus `verified`, diurutkan dari harga termurah.
2. Paket nonaktif, desa `pending`, desa `rejected`, dan desa `suspended` tidak muncul di beranda, halaman desa, maupun halaman paket. Semuanya dijawab 404.
3. Halaman desa (`/desa/{slug}`) menampilkan nama, kecamatan, kabupaten, deskripsi, foto, kontak, dan daftar paket aktif.
4. Halaman paket menampilkan harga per orang, durasi ("2 hari 1 malam" atau "1 hari"), jenis kegiatan, foto, dan tombol "Pilih tanggal dan pesan".
5. Semua halaman di atas terbuka tanpa login.

**Bukti:** `TestHomeSearch`, `TestSearch`, `TestManagerFlowAndPublicVisibility`, `TestPublicVisibility`, `TestPublicVisibilityAndCards`, `TestPagesRenderAndNotFound`.

#### W-02 Mencari dan menyaring paket

*Sebagai wisatawan, saya ingin menyaring paket menurut daerah, kegiatan, harga, dan kata kunci, agar saya cepat menemukan yang cocok.*

1. **Filter yang tersedia:** kabupaten/kota, jenis kegiatan, harga minimum, harga maksimum, dan kata kunci. Kata kunci dicocokkan dengan nama paket atau nama desa, tanpa membedakan huruf besar-kecil.
2. **Format harga:** "150000", "150.000", dan "Rp 150.000" dianggap sama.
3. **Karakter khusus:** `%` dan `_` dalam kata kunci dicari apa adanya, bukan sebagai wildcard.
4. **Nilai tidak valid** (misalnya huruf di kolom harga) diabaikan tanpa error.
5. **Halaman:** satu halaman berisi 24 hasil, dengan tautan "Sebelumnya" dan "Berikutnya" yang mempertahankan filter.
6. **Hasil kosong:** ditampilkan pesan "Tidak ada paket yang cocok" beserta tautan "Hapus filter".

**Bukti:** `TestSearch`, `TestHomeSearch`.

#### W-03 Mendaftar dan masuk

*Sebagai wisatawan, saya ingin membuat akun dan masuk dengan aman, agar saya bisa memesan dan melihat riwayat pemesanan.*

1. **Data pendaftaran:** nama (1–100 karakter), email, kata sandi (8–128 karakter), dan nomor HP (opsional). Setiap field yang salah diberi pesan di bawahnya.
2. **Email unik tanpa membedakan huruf besar-kecil:** `Sari@Contoh.com` dan `sari@contoh.com` dianggap sama.
3. **Gagal masuk:** pesannya selalu "Email atau kata sandi salah", tanpa memberi tahu mana yang salah. Waktu prosesnya juga sama, baik email terdaftar maupun tidak.
4. **Setelah berhasil masuk:**
   - token session diganti (mencegah *session fixation*),
   - pengguna kembali ke halaman asal, tetapi hanya jika alamatnya path internal. Alamat seperti `//evil.com`, `https://…`, atau `/\evil.com` diarahkan ke `/`.
5. **Cookie session:** `HttpOnly` dan `SameSite=Lax`, ditambah `Secure` di produksi.
6. **Batas percobaan:** lebih dari 5 percobaan gagal per menit untuk pasangan IP+email yang sama dijawab 429 dengan header `Retry-After`.

**Bukti:** `TestRegister`, `TestAuthenticate`, `TestRegisterLoginLogout`, `TestLoginRedirectOnlyInternal`, `TestSafeRedirect`, `TestSessionCookieAttributes`, `TestLoginRateLimit`, `TestPasswordHashAndVerify`.

#### W-04 Melihat tanggal yang tersedia

*Sebagai wisatawan, saya ingin melihat tanggal mana yang masih ada kuotanya, agar saya tidak memesan tanggal yang penuh.*

1. Halaman `/pesan/paket/{id}` menampilkan kalender bulanan, dengan Senin sebagai hari pertama.
2. **Tanggal yang bisa dipilih:** lusa s.d. hari ini + 90, menurut zona waktu Asia/Jakarta, dan masih punya sisa kuota. Setiap tanggal menampilkan "sisa N".
3. **Tanggal yang tidak bisa dipilih:** tanggal yang lewat, hari ini, besok, tanggal yang ditutup ("Tutup"), dan tanggal yang penuh ("Penuh"). Semuanya tampil abu-abu.
4. **Navigasi bulan:** dibatasi pada bulan yang beririsan dengan jendela pemesanan. Navigasi tetap berfungsi tanpa JavaScript, dan dengan HTMX hanya kalendernya yang diganti.
5. **Paket yang tidak bisa dipesan:** halaman pemesanan untuk paket nonaktif atau desa yang belum terverifikasi dijawab 404.

**Bukti:** `TestMonthGrid`, `TestCalendar`, `TestTravelerBookingFlow`, `TestBookingPageHiddenForUnbookablePackages`, `TestCreateDateWindow`. **Manual:** pergantian kalender lewat HTMX.

#### W-05 Mengirim permintaan pemesanan

*Sebagai wisatawan, saya ingin mengirim permintaan berisi tanggal, jumlah orang, dan catatan, agar pengelola desa bisa menyiapkan kunjungan saya.*

1. **Pengunjung yang belum masuk** melihat tombol "Masuk untuk memesan", bukan form. Setelah masuk, pengunjung kembali ke halaman pemesanan.
2. **Isi form:** tanggal (dipilih di kalender), jumlah orang (1–1000), nama pemesan, nomor HP (wajib), dan catatan (opsional, maksimal 1000 karakter). Nama dan nomor HP terisi otomatis dari profil.
3. **Saat permintaan berhasil:**
   - kuota tanggal itu langsung ditahan sejumlah orang yang dipesan,
   - status permintaan menjadi `pending`,
   - total harga dicatat saat itu juga, sehingga perubahan harga paket sesudahnya tidak memengaruhi permintaan ini,
   - wisatawan diarahkan ke halaman detail permintaan.
4. **Pesan gagal yang jelas:**
   - tanggal di luar jendela,
   - tanggal ditutup,
   - kuota tidak cukup ("Pilih tanggal lain atau kurangi jumlah orang"),
   - sudah ada 3 permintaan `pending`.
5. **Batas pengiriman:** maksimal 10 permintaan per jam per akun.

**Bukti:** `TestCreateReservesQuotaAndSnapshotsPrice`, `TestCreateValidation`, `TestCreateDateWindow`, `TestCreateRejectsUnbookable`, `TestPendingLimitUnderConcurrency`, `TestClosingDateKeepsExistingBookings`, `TestTravelerBookingFlow`. **Manual:** batas 10 permintaan per jam (mekanisme limiter-nya dites di `TestLimiter` dan `TestLoginRateLimit`).

#### W-06 Memantau status pemesanan

*Sebagai wisatawan, saya ingin melihat status setiap permintaan saya, agar saya tahu apakah kunjungan saya jadi.*

1. "Pemesanan saya" (`/pemesanan`) menampilkan semua pemesanan milik pengguna, dari tanggal keberangkatan terbaru.
2. **Detail pemesanan** menampilkan:
   - status dengan label berwarna,
   - paket, desa, tanggal (misalnya "Sabtu, 15 Maret 2030"), jumlah orang, dan perkiraan total,
   - catatan dan keterangan dari pengelola.
3. **Saat status `pending`,** detail menjelaskan batas waktu tanggapan pengelola dan bahwa permintaan otomatis kedaluwarsa jika lewat batas itu.
4. **Kontak desa** ditampilkan beserta tombol "Tanya desa via WhatsApp" yang berisi pesan siap kirim.
5. **Pemesanan orang lain** dijawab 404.

**Bukti:** `TestTravelerBookingFlow`, `TestManagerHandlesBooking`, `TestBookingAccessOverHTTP`, `TestAccessAcrossVillagesAndTravelers`.

#### W-07 Membatalkan pemesanan

*Sebagai wisatawan, saya ingin membatalkan pemesanan yang tidak jadi, agar kuotanya bisa dipakai orang lain.*

1. Tombol batal hanya muncul untuk pemesanan `pending` atau `confirmed` yang tanggal keberangkatannya hari ini atau nanti.
2. Alasan pembatalan boleh diisi atau dikosongkan.
3. Setelah dibatalkan, status menjadi `cancelled` dengan catatan "dibatalkan oleh wisatawan", dan kuota langsung kembali.
4. Membatalkan setelah tanggal keberangkatan lewat, atau membatalkan pemesanan yang sudah selesai, ditolak.

**Bukti:** `TestCancelAfterConfirmReleasesQuota`, `TestTransitions`, `TestTravelerBookingFlow`.

#### W-08 Melaporkan konten bermasalah

*Sebagai wisatawan, saya ingin melaporkan desa atau paket yang mencurigakan, agar admin bisa menindaklanjuti dugaan penipuan.*

1. Form "Laporkan konten ini" hanya muncul untuk pengguna yang sudah masuk, baik di halaman desa maupun halaman paket.
2. Alasan wajib diisi, maksimal 1000 karakter.
3. Laporan untuk desa atau paket yang tidak ada dijawab 404.
4. Setelah mengirim, pengguna kembali ke halaman yang sama (hanya path internal) dengan pesan terima kasih.
5. Batas pengiriman: maksimal 5 laporan per jam per akun.

**Bukti:** `TestReportFlow`, `TestReports`. **Manual:** batas 5 laporan per jam.

#### W-09 Menghapus akun sendiri

*Sebagai pengguna, saya ingin menghapus akun dan data pribadi saya, agar data saya tidak tersimpan lebih lama dari yang saya mau.*

1. Penghapusan memerlukan kata sandi.
2. Penghapusan ditolak jika pengguna adalah pengelola terakhir sebuah desa. Aturan ini tetap berlaku walaupun dua pengelola menghapus akun pada saat bersamaan.
3. **Setelah akun dihapus:**
   - pemesanan tetap tersimpan sebagai catatan desa, tetapi nama diganti "Akun dihapus", sedangkan nomor HP dan catatan dikosongkan,
   - laporan yang pernah dikirim kehilangan identitas pelapor,
   - semua sesi akun itu tidak lagi dianggap masuk,
   - akun itu tidak bisa dipakai untuk masuk lagi.

**Bukti:** `TestDeleteAccount`, `TestDeleteAccountFlow`, `TestSchemaConstraints`.

### 10.2 Pengelola desa

#### P-01 Mendaftarkan desa

*Sebagai pengelola desa wisata, saya ingin mendaftarkan desa saya, agar desa bisa tampil dan menerima pemesanan setelah diverifikasi.*

1. **Data desa:** nama, kecamatan (dipilih dari daftar), nomor telepon (wajib), WhatsApp (opsional), alamat, dan deskripsi.
2. **Setelah terdaftar:**
   - desa berstatus `pending`,
   - pendaftar otomatis menjadi pengelolanya,
   - alamat web (slug) dibuat dari nama desa dan selalu unik (`desa-wisata-contoh`, `desa-wisata-contoh-2`, …).
3. **Selama belum `verified`,** dashboard menampilkan spanduk status. Desa yang ditolak juga menampilkan alasan penolakannya.
4. **Pratinjau:** profil dan paket tetap bisa diisi, dan pengelola bisa melihat pratinjau halaman publik.

**Bukti:** `TestCreateAndManage`, `TestCreateValidation`, `TestSlugify`, `TestManagerFlowAndPublicVisibility`.

#### P-02 Mengelola profil dan foto desa

*Sebagai pengelola, saya ingin memperbarui profil dan foto desa, agar wisatawan mendapat gambaran yang benar.*

1. Mengubah nama desa tidak mengubah alamat web (slug), jadi tautan yang sudah dibagikan tetap berfungsi.
2. **Syarat foto:**
   - Setiap unggahan wajib mencentang pernyataan persetujuan warga dan pengelola.
   - Ukuran maksimal 5 MB.
   - Hanya JPEG, PNG, atau WebP. Jenis file diperiksa dari isinya, bukan dari ekstensinya.
   - Resolusi maksimal 6000×6000 piksel.
3. **Pengolahan foto:**
   - Rotasi foto dari HP diikuti, lalu foto diperkecil ke sisi terpanjang 1600 px.
   - Semua metadata dihapus, termasuk lokasi GPS.
4. **Batas jumlah:** maksimal 12 foto. Foto bisa dihapus, dan file-nya ikut terhapus.
5. **Pesan gagal yang jelas:** foto terlalu besar, format tidak didukung, file rusak, persetujuan belum dicentang, atau batas jumlah tercapai.

**Bukti:** `TestPhotos`, `TestPhotoUpload`, `TestProcessRejects`, `TestProcessStripsExifAndAppliesOrientation`, `TestProcessDownscalesLargeImages`, `TestLocalStoreAndServe`, `TestCreateAndManage`.

#### P-03 Mengelola paket wisata

*Sebagai pengelola, saya ingin membuat dan mengubah paket kegiatan, agar wisatawan bisa membandingkan pilihan dan harganya.*

1. **Data paket:**
   - nama dan deskripsi,
   - harga per orang (format rupiah bebas),
   - lama kegiatan 1–30 hari, dengan jumlah malam tidak melebihi jumlah hari,
   - kuota harian 1–1000 orang,
   - jenis kegiatan (pilihan ganda).
2. **Aktif/nonaktif:** paket bisa diaktifkan dan dinonaktifkan. Paket nonaktif hilang dari katalog, tetapi pengelola masih bisa melihat pratinjaunya.
3. **Tidak ada hapus:** paket tidak bisa dihapus, karena mungkin sudah punya riwayat pemesanan.
4. **Perubahan setelah ada pemesanan:**
   - perubahan harga tidak mengubah total pemesanan yang sudah ada,
   - perubahan kuota harian hanya berlaku untuk tanggal yang belum pernah diubah atau dipesan.

**Bukti:** `TestCreateUpdateGet`, `TestValidation`, `TestPublicVisibilityAndCards`, `TestCreateReservesQuotaAndSnapshotsPrice`.

#### P-04 Mengatur kalender ketersediaan

*Sebagai pengelola, saya ingin mengatur kuota dan menutup tanggal tertentu, agar desa tidak menerima tamu melebihi kemampuannya.*

1. Kalender per paket menampilkan setiap tanggal sebagai "jumlah yang memesan / kuota".
2. Pengelola mengetuk sebuah tanggal untuk mengubah kuotanya atau menutup/membuka tanggal itu. Perubahan hanya bisa dilakukan untuk rentang hari ini s.d. hari ini + 90.
3. Kuota tidak bisa diturunkan di bawah jumlah orang yang sudah memesan, dan pengelola mendapat pesan yang menjelaskan alasannya.
4. Menutup tanggal hanya menghentikan pemesanan baru. Pemesanan yang sudah ada tidak berubah, dan permintaan `pending` di tanggal itu tetap bisa dikonfirmasi.

**Bukti:** `TestQuotaCannotGoBelowReserved`, `TestClosingDateKeepsExistingBookings`, `TestManagerCalendar`, `TestCalendar`.

#### P-05 Menanggapi permintaan pemesanan

*Sebagai pengelola, saya ingin melihat permintaan yang perlu ditanggapi dan menjawabnya dengan cepat, agar wisatawan tidak menunggu terlalu lama.*

1. **Beranda desa** menampilkan tiga daftar:
   - **Perlu ditanggapi:** diurutkan dari yang paling dekat kedaluwarsa, dengan keterangan "sisa N jam".
   - **Kunjungan mendatang.**
   - **Perlu ditandai selesai.**
2. **Detail permintaan** menampilkan:
   - kontak wisatawan,
   - batas waktu tanggapan,
   - hanya tombol untuk tindakan yang sah saat itu.
3. **Konfirmasi:**
   - hanya bisa dilakukan sebelum batas waktu,
   - kuota tetap ditahan,
   - tombol Konfirmasi hilang setelah dipakai.
4. **Tolak:**
   - alasan boleh diisi atau dikosongkan,
   - kuota langsung kembali,
   - alasan terlihat oleh wisatawan.
5. **Status yang sudah berubah:** jika status lebih dulu berubah, misalnya sudah kedaluwarsa atau sudah dikonfirmasi, pengelola mendapat pesan "Status pemesanan sudah berubah" dan tidak ada data yang rusak.

**Bukti:** `TestManagerHandlesBooking`, `TestActionsFor`, `TestTransitions`, `TestRejectRacesExpiry`.

#### P-06 Mengabari wisatawan lewat WhatsApp

*Sebagai pengelola yang terbiasa memakai WhatsApp, saya ingin mengabari wisatawan dengan satu ketukan, agar tidak perlu mengetik ulang detail pemesanan.*

1. Tombol "Kabari via WhatsApp" membuka `https://wa.me/62…` dengan nomor wisatawan yang sudah dinormalisasi. Contohnya, `0812-3456-7890`, `+62 812…`, dan `812…` semuanya menjadi `62812…`.
2. Isi pesan mengikuti status pemesanan (diterima, dikonfirmasi beserta total, ditolak, dibatalkan, kedaluwarsa, atau selesai) dan menyebut nama wisatawan, desa, paket, tanggal, dan jumlah orang.
3. Pesan di-URL-encode dengan benar, termasuk spasi, `&`, `#`, dan tanda kutip.
4. Tombol tidak muncul jika nomornya tidak valid atau akun wisatawan sudah dihapus.

**Bukti:** `TestNormalizeWA`, `TestWALink`, `TestManagerHandlesBooking`.

#### P-07 Menyelesaikan dan membatalkan kunjungan

*Sebagai pengelola, saya ingin menandai kunjungan selesai atau membatalkannya jika ada kendala, agar catatan desa rapi.*

1. **Tandai selesai** bisa dilakukan pada atau setelah tanggal keberangkatan, untuk pemesanan `confirmed`. Kuotanya tetap terhitung terpakai.
2. **Membatalkan pemesanan `confirmed`** bisa dilakukan sampai hari keberangkatan, misalnya karena cuaca buruk. Kuotanya kembali, dan alasannya terlihat oleh wisatawan.
3. Pemesanan yang sudah selesai tidak bisa dibatalkan.

**Bukti:** `TestTransitions`, `TestCancelAfterConfirmReleasesQuota`, `TestManagerHandlesBooking`.

#### P-08 Data desa terpisah dari desa lain

*Sebagai pengelola, saya ingin data desa saya tidak bisa dilihat atau diubah pengelola desa lain, agar pemesanan dan kontak tamu terlindungi.*

1. Semua halaman dan aksi di `/kelola/desa/{id}/…` dijawab 404 untuk pengguna yang bukan pengelola desa itu. Ini mencakup profil, foto, paket, kalender, pemesanan, dan panel.
2. ID paket atau pemesanan milik desa lain yang dimasukkan di bawah ID desa sendiri juga dijawab 404.
3. Setiap aksi pemesanan memeriksa bahwa pemesanan itu milik desa yang dikelola, bukan hanya bahwa pengguna adalah seorang pengelola.

**Bukti:** `TestManagePagesRejectOtherUsers`, `TestBookingAccessOverHTTP`, `TestAccessAcrossVillagesAndTravelers`, `TestCreateUpdateGet`.

### 10.3 Admin platform

#### A-01 Menjadi admin

*Sebagai pemilik platform, saya ingin menunjuk admin tanpa form publik, agar tidak ada yang bisa mengangkat dirinya sendiri.*

1. Admin dibuat dengan `desakita promote-admin EMAIL` untuk akun yang sudah terdaftar. Email dicocokkan tanpa membedakan huruf besar-kecil.
2. Email yang belum terdaftar ditolak dengan pesan error.

**Bukti:** `TestPromoteAdmin`.

#### A-02 Memverifikasi atau menolak desa

*Sebagai admin, saya ingin memeriksa desa yang baru mendaftar, agar tidak ada desa palsu di katalog.*

1. Dashboard admin menampilkan antrean desa `pending`, ditambah tab untuk desa `verified`, `suspended`, dan `rejected`.
2. Detail desa menampilkan profil, kontak, foto, dan daftar pengelola beserta email dan nomor HP-nya.
3. **Verifikasi** membuat desa tampil di katalog dan mencatat admin yang memverifikasi serta waktunya.
4. **Tolak** memerlukan alasan, dan alasan itu terlihat oleh pengelola. Desa yang ditolak bisa diverifikasi kemudian jika datanya sudah diperbaiki.
5. Dua admin yang bertindak bersamaan tidak bisa melompati status. Admin yang kedua mendapat pesan "Status desa sudah berubah".

**Bukti:** `TestAdminStatusChanges`, `TestAdminVerifiesAndSuspends`. Kriteria 5 baru dites secara berurutan (verifikasi dua kali ditolak). Belum ada tes untuk dua admin yang bertindak bersamaan.

#### A-03 Menonaktifkan dan mengaktifkan kembali desa

*Sebagai admin, saya ingin menonaktifkan desa yang bermasalah, agar wisatawan baru tidak tertipu.*

1. Desa `verified` bisa dinonaktifkan (`suspended`) dengan alasan opsional.
2. **Akibat penonaktifan:**
   - desa dan paketnya hilang dari katalog,
   - halaman pemesanan dijawab 404,
   - permintaan baru ditolak,
   - pemesanan yang sudah ada tidak dibatalkan otomatis,
   - pengelola melihat spanduk "dinonaktifkan sementara".
3. Desa `suspended` bisa diaktifkan kembali.

**Bukti:** `TestAdminVerifiesAndSuspends`, `TestAdminStatusChanges`.

#### A-04 Menangani laporan

*Sebagai admin, saya ingin melihat dan menutup laporan pengguna, agar dugaan penipuan tidak terlewat.*

1. Halaman laporan menampilkan laporan `open` beserta tanggal, email pelapor ("akun dihapus" jika sudah dihapus), tautan ke desa atau paket yang dilaporkan, dan alasannya.
2. Laporan bisa ditandai "Selesai" atau "Abaikan". Admin yang menutup dan waktunya dicatat.
3. Laporan yang sudah ditutup tidak bisa ditutup dua kali.

**Bukti:** `TestReports`, `TestReportFlow`.

#### A-05 Area admin tersembunyi

*Sebagai pemilik platform, saya ingin area admin tidak terlihat oleh pengguna biasa, agar permukaan serangan kecil.*

1. Semua URL `/admin…` dijawab 404, bukan 403, untuk pengunjung anonim dan pengguna non-admin.
2. Tautan "Admin" di navigasi hanya muncul untuk admin.

**Bukti:** `TestAdminAreaHiddenFromNonAdmins`, `TestNewAdminPagesHiddenFromNonAdmins`, `TestRequireAdmin`.

#### A-06 Log audit

*Sebagai pemilik platform, saya ingin setiap tindakan admin tercatat permanen, agar keputusan moderasi bisa dipertanggungjawabkan.*

1. **Aksi yang dicatat:** verifikasi, tolak, nonaktifkan, dan aktifkan kembali desa; penyelesaian dan pengabaian laporan; serta pengangkatan admin lewat CLI.
2. **Isi entri:** pelaku, aksi, target, status asal dan tujuan, catatan, dan waktu. Waktu ditampilkan dalam WIB.
3. **Atomik:** entri hanya ada jika perubahannya berhasil. Tindakan yang ditolak (misalnya status sudah berubah) tidak tercatat, dan transaksi yang di-rollback tidak meninggalkan entri.
4. **Tindakan bersamaan:** dua admin yang memverifikasi desa yang sama secara bersamaan menghasilkan tepat satu perubahan dan satu entri.
5. **Append-only:** entri tidak bisa diubah, dihapus, atau di-truncate, bahkan langsung lewat SQL. Entri tetap ada setelah akun admin dihapus.
6. **Halaman audit:** filter aksi dan jenis objek, pagination, dan riwayat per desa di halaman detail desa.

**Bukti:** `TestAuditLogIsAppendOnly`, `TestAuditRolledBackWithTransaction`, `TestAuditFilters`, `TestStatusChangesAreAudited`, `TestConcurrentAdminsAuditOnce`, `TestReports`, `TestPromoteAdmin`, `TestAdminDashboardAuditTrail`, dan E2E `admin.spec.ts`.

### 10.4 Sistem

#### S-01 Permintaan kedaluwarsa otomatis

1. **Batas waktu:** setiap permintaan `pending` punya batas waktu, yaitu mana yang lebih dulu antara 48 jam setelah dikirim atau pukul 00:00 WIB pada hari keberangkatan. Nilai 48 jam bisa diatur lewat konfigurasi.
2. **Job kedaluwarsa:** setiap 5 menit, sebuah job mengubah permintaan yang lewat batas menjadi `expired` dan mengembalikan kuotanya.
3. **Keamanan job:** job aman dijalankan bersamaan dengan tindakan pengelola. Jika pengelola menolak tepat saat job berjalan, hanya satu yang berhasil dan kuota dikembalikan tepat satu kali.

**Bukti:** `TestExpiresAt`, `TestExpireDue`, `TestRejectRacesExpiry`.

#### S-02 Kuota tidak pernah terjual melebihi batas

1. Dua puluh wisatawan yang memesan kuota terakhir pada saat bersamaan hanya menghasilkan satu pemesanan berhasil. `reserved` tidak pernah melebihi `quota`.
2. Satu akun yang mengirim banyak permintaan bersamaan hanya mendapat maksimal 3 permintaan `pending`.
3. Selama permintaan diproses, status paket dan desa tidak bisa berubah di tengah jalan karena dikunci dengan `FOR SHARE`.
4. **Pengaman kedua:** constraint database (`CHECK 0 ≤ reserved ≤ quota`) tetap menjaga data walaupun ada bug di kode.

**Bukti:** `TestConcurrentBookingForLastQuota`, `TestPendingLimitUnderConcurrency`, `TestPackageLookupAdapter`, `TestSchemaConstraints`. Kedua tes konkurensi utama sudah diverifikasi lewat *mutation check*: tes gagal jika advisory lock atau syarat status dihapus.

#### S-03 Keamanan dasar

1. **CSRF:** setiap request yang mengubah data tanpa token CSRF yang cocok ditolak dengan 403. Ini juga berlaku untuk unggahan multipart.
2. **Body terlalu besar:** request yang melebihi batas (form 1 MiB, unggahan 6 MiB) dijawab 413 dengan pesan yang jelas.
3. **Header keamanan:** setiap respons membawa CSP ketat (tanpa skrip atau style inline), `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy`, dan `Permissions-Policy`. HSTS ditambahkan di produksi.
4. **IP klien:** `X-Forwarded-For` palsu tidak bisa dipakai untuk menghindari rate limit, kecuali `TRUST_PROXY=true` diaktifkan di belakang reverse proxy.
5. **Galat server:** pengguna hanya melihat halaman generik beserta kode permintaan. Detailnya hanya ada di log, dan log tidak pernah memuat kata sandi, nomor HP, atau catatan pemesanan.

**Bukti:** `TestProtect`, `TestPostWithoutCSRFIsRejected`, `TestPhotoUpload`, `TestSecurityHeaders`, `TestSpoofedForwardedForDoesNotBypassLimit`, `TestClientIP`. **Manual:** isi log.

#### S-04 Data dan skema terjaga

1. Migrasi bisa dijalankan naik, turun, lalu naik lagi tanpa error. Seed pengembangan aman dijalankan berulang kali.
2. **Constraint database menolak data yang tidak sah:**
   - email ganda (tanpa membedakan huruf besar-kecil),
   - foto tanpa target atau dengan dua target,
   - foto tanpa persetujuan,
   - pemesanan yang desanya tidak cocok dengan desa pemilik paket,
   - status `cancelled` tanpa keterangan siapa yang membatalkan.
3. Setiap teks UI punya terjemahan. Tes gagal jika ada kunci yang hilang.

**Bukti:** `TestMigrateUpDownUp`, `TestSeedWithGeneratedQueries`, `TestSchemaConstraints`, `TestI18nKeysExist`.

### 10.5 Kriteria yang belum terpenuhi

Kriteria berikut belum terpenuhi atau belum terverifikasi, dan dicatat di sini supaya tidak dianggap selesai:

- **Panduan bergambar dan kontak bantuan** untuk pengelola (§8): *belum* dibuat.
- **`go test -race`:** *belum* dijalankan karena WSL belum memiliki `gcc`.
- **Build Docker** dan `docker compose up`: *belum* dicoba.
- **Tampilan di HP** dan HTMX di bawah CSP: baru dicek secara manual oleh pemilik proyek, tanpa tes otomatis.

## 11. Pencarian dan filter (tahap 3)

### Filter

Semua filter bisa digabung:

| Filter | Parameter | Keterangan |
|---|---|---|
| Kabupaten/kota | `wilayah` | |
| Jenis kegiatan | `kegiatan` | |
| Harga minimum dan maksimum | `harga_min`, `harga_maks` | format rupiah bebas |
| Kata kunci | `q` | maksimal 100 karakter |

Nilai yang tidak valid diabaikan. Setiap halaman berisi 24 hasil, dan jumlah total hasil selalu ditampilkan.

### Kata kunci

- **Full-text PostgreSQL** pada nama paket (bobot A), deskripsi paket (B), serta nama (A) dan deskripsi desa (C).
  - Konfigurasi yang dipakai adalah `simple`: huruf kecil tanpa stemming, karena PostgreSQL tidak menyediakan kamus Bahasa Indonesia.
  - Sintaks `websearch_to_tsquery` didukung: semua kata harus cocok, `"frasa"`, `or`, dan `-kecualikan`.
- **Sebagian kata pada nama** paket atau desa dicocokkan dengan `pg_trgm` (`ILIKE`), misalnya "atik" cocok dengan "Membatik". Wildcard `%` dan `_` dari pengguna di-escape.
- Hasil adalah gabungan keduanya (salah satu cocok sudah cukup).

### Urutan (`urut`)

| Nilai | Urutan |
|---|---|
| `relevan` | default jika ada kata kunci |
| `termurah` | default jika tanpa kata kunci |
| `termahal` | |
| `terbaru` | |

- **Skor relevansi:** `2 × ts_rank(paket) + ts_rank(desa) + word_similarity(kata kunci, nama paket)`. Cocok di nama paket mengalahkan cocok di deskripsi.
- **Nilai tak dikenal:** `relevan` tanpa kata kunci, atau nilai yang tidak dikenal, kembali ke urutan default.

### Indeks (`migrations/000004`)

- **Kolom pencarian:** `search_vector` (tsvector yang di-generate otomatis) di `tour_packages` dan `villages`, masing-masing dengan indeks GIN.
- **Trigram:** indeks GIN trigram pada `name` di kedua tabel.
- **Indeks parsial:** `tour_packages(village_id) WHERE is_active`.

**Bentuk query:** kandidat hasil dikumpulkan lewat `UNION` dua cabang (cocok di paket dan cocok di desa), dan teks query dipakai langsung, tidak lewat kolom CTE. Dua hal ini membuat planner bisa membangun *bitmap index scan*.

**Diverifikasi dengan `EXPLAIN` pada SQL yang benar-benar dijalankan:**
- Pada 30.000 paket, planner memilih sendiri kedua indeks paket. Waktu pencarian sekitar 13 ms di mesin pengembangan.
- Indeks desa bisa dipakai oleh kondisi pencarian desa. Pada ribuan desa, *seq scan* memang lebih murah dan dipilih planner. Indeks desa baru menguntungkan di skala yang jauh lebih besar.

**Bukti:** `TestFullTextSearch`, `TestSearchPaginationTotal`, `TestSearchUsesIndexes`, `TestSearch`, `TestHomeSearchSortAndTotal`.

## 12. Ulasan dan rating (tahap 4)

### Siapa dan kapan

- Hanya wisatawan pemilik pemesanan yang **sudah selesai** (`completed`) yang bisa menulis ulasan.
- Setiap pemesanan hanya bisa diulas satu kali (`UNIQUE(booking_id)`).
- Syarat ini dicek di dalam transaksi, dengan pemesanan dikunci `FOR SHARE` lewat interface `review.Bookings` (adapternya ada di `internal/app`).

### Isi ulasan

- Rating 1–5 bintang dan teks 10–2000 karakter.
- Penulis bisa menghapus ulasannya sendiri. Setelah dihapus, pemesanan itu bisa diulas lagi.

### Tampilan

- **Halaman paket:** rata-rata bintang, jumlah ulasan, dan sebaran 5→1 bintang. Batang sebaran digambar dengan SVG supaya aman terhadap CSP.
- **Daftar ulasan:** 10 ulasan per halaman, dari yang terbaru.
- **Kartu paket:** "★ 4,6 (12 ulasan)".
- **Nama penulis:** nama depan dan inisial nama belakang ("Sari W.").
- **Akun penulis dihapus:** ulasan tetap ada atas nama "Mantan pengguna".

### Moderasi pasca-terbit

- Ulasan langsung tampil, karena penulisnya sudah terbukti berkunjung.
- Pengguna yang login bisa melaporkan ulasan (`reports.target_type = 'review'`).
- Admin (`/admin/ulasan`, dengan tab tampil, dilaporkan, dan disembunyikan) bisa:
  - **menyembunyikan** ulasan dengan alasan wajib, yang juga terlihat oleh penulis;
  - **menampilkan kembali** ulasan.
- Kedua tindakan dikunci per baris dan dicatat di log audit (`review.hide` dan `review.restore`) dalam transaksi yang sama.
- Ulasan yang disembunyikan tidak dihitung dalam rating.

### Kriteria dan bukti

| Kriteria | Tes |
|---|---|
| Hanya pemesanan sendiri yang sudah selesai yang bisa diulas; pemesanan orang lain dijawab 404, status lain ditolak | `TestOnlyOwnCompletedBookingsCanBeReviewed` |
| Lima kiriman bersamaan untuk pemesanan yang sama menghasilkan tepat satu ulasan | `TestConcurrentSubmissionsCreateOneReview` |
| Validasi rating dan panjang teks | `TestValidation` |
| Rata-rata, jumlah, sebaran, dan nama penulis yang disingkat; penghapusan akun membuat ulasan anonim | `TestSummaryListingAndAnonymisation` |
| Pagination 10 ulasan per halaman | `TestPagination` |
| Sembunyikan memerlukan alasan, tercatat di audit, tidak bisa dilakukan dua kali, dan ulasan keluar dari rating; tampilkan kembali juga tercatat | `TestModerationIsAudited` |
| Penulis hanya bisa menghapus ulasannya sendiri | `TestAuthorDeletesOwnReviewOnly` |
| Penyingkatan nama | `TestDisplayName` |
| Alur HTTP lengkap: belum selesai → tanpa form; ulasan tampil di halaman dan kartu; laporan; admin menyembunyikan dan penulis melihat alasannya; non-admin 404 | `TestReviewJourney` |
| Alur E2E dengan pemilihan rating lewat keyboard, serta axe di form, halaman paket, dan moderasi | `review.spec.ts`, `a11y.spec.ts` |

**Keputusan untuk ditinjau:**
- Moderasi dilakukan **pasca-terbit**, bukan pra-moderasi.
- Tidak ada batas waktu untuk menulis ulasan setelah kunjungan.
- Pengelola tidak bisa membalas ulasan (di luar cakupan).

## 13. Notifikasi email (tahap 5)

### Pola outbox

- Email **tidak** dikirim di dalam request. Email ditulis ke tabel `email_outbox` di **transaksi yang sama** dengan perubahan bisnisnya (pemesanan dibuat, status berubah, status desa berubah).
  - Jika transaksi di-rollback, email ikut hilang.
  - Jika email gagal dimasukkan ke antrean, perubahan bisnis ikut dibatalkan.
- Worker (`mail.Worker`, dijalankan oleh `serve` setiap 15 detik):
  - mengambil maksimal 20 email yang jatuh tempo dengan `FOR UPDATE SKIP LOCKED`, lalu menandainya `sending` dengan kunci 5 menit; dua worker tidak pernah mengirim email yang sama;
  - mengirim email di luar transaksi, lalu menandai `sent`;
  - jika gagal, mencoba lagi setelah 1 m, 5 m, 30 m, 2 j, lalu 6 j. Setelah 6 percobaan, status menjadi `failed`;
  - mengambil ulang email `sending` yang kuncinya habis (worker mati di tengah jalan).
- Pengiriman bersifat **at-least-once**. Header `Message-ID` tetap sama untuk setiap baris outbox (`<outbox-ID@domain>`), sehingga pengiriman ulang bisa dikenali sebagai duplikat oleh server penerima.
- `dedupe_key` unik mencegah email ganda untuk peristiwa yang sama (misalnya `booking:12:confirmed:email`).
- Email terkirim dihapus setelah 30 hari. Email yang belum terkirim untuk akun yang dihapus langsung dihapus.

### Peristiwa dan penerima

| Peristiwa | Penerima | Isi utama |
|---|---|---|
| Permintaan pemesanan dibuat | semua pengelola desa, wisatawan | paket, tanggal, jumlah orang, perkiraan total, batas waktu tanggapan |
| Dikonfirmasi | wisatawan | detail pemesanan dan tautan |
| Ditolak | wisatawan | alasan dari pengelola (jika ada) |
| Dibatalkan wisatawan | semua pengelola desa | detail pemesanan |
| Dibatalkan pengelola | wisatawan | alasan (jika ada) |
| Kedaluwarsa | wisatawan | ajakan memilih tanggal atau paket lain |
| Selesai | wisatawan | tautan menulis ulasan |
| Desa diverifikasi / ditolak / dinonaktifkan / diaktifkan kembali | semua pengelola desa | alasan dari admin (jika ada), tautan |

### Isi dan privasi

- Email berupa teks biasa (UTF-8, quoted-printable), dari template `web/emails/id/*.txt`. Setiap file punya bagian `subject` dan `body`. Bahasa baru cukup menambah folder.
- Email **tidak pernah** memuat nomor HP, catatan pemesanan, atau kata sandi. Footer menegaskan bahwa DesaKita tidak pernah meminta pembayaran lewat email.
- Log worker hanya mencatat ID outbox, jenis, nomor percobaan, dan pesan galat. Alamat email dan isi email tidak dicatat.
- Header `Auto-Submitted: auto-generated` mencegah balasan otomatis.

### Pengiriman

- Jika `SMTP_HOST` diisi, email dikirim lewat SMTP. `SMTP_TLS=starttls` (default) **mewajibkan** STARTTLS; `tls` memakai TLS langsung (port 465); `none` hanya untuk server lokal.
- Jika `SMTP_HOST` kosong, email ditulis sebagai file `.eml` ke `MAIL_DIR` (untuk pengembangan). Di production, aplikasi memberi peringatan di log.
- Tautan di email memakai `APP_BASE_URL`.

### Kriteria dan bukti

| Kriteria | Tes |
|---|---|
| Email masuk antrean hanya jika transaksi commit; `dedupe_key` mencegah duplikat | `TestEnqueueIsTransactionalAndDeduplicated` |
| Setiap transisi sah memicu tepat satu peristiwa; transisi tidak sah tidak memicu apa pun | `TestNotifierSeesEachTransitionOnce` |
| Gagal memasukkan email ke antrean membatalkan perubahan bisnis | `TestNotifierFailureRollsBackTheChange` |
| Penerima, isi, tautan, dan alasan sesuai peristiwa; nomor HP dan catatan tidak ikut | `TestBookingEmailsAreQueued`, `TestRejectAndCancelEmails`, `TestVillageStatusEmails` |
| Retry dengan backoff, lalu `failed` setelah 6 percobaan | `TestWorkerDeliversRetriesAndGivesUp` |
| Email `sending` yang macet diambil ulang dengan `Message-ID` yang sama | `TestStaleSendingIsReclaimedWithSameMessageID` |
| Empat worker bersamaan mengirim setiap email tepat satu kali | `TestConcurrentWorkersSendEachMessageOnce` |
| Retensi 30 hari; akun dihapus tidak menyisakan email yang belum terkirim | `TestPurgeSentAndAccountData`, `TestDeletedAccountGetsNoQueuedMail` |
| Header aman dari injeksi CR/LF; subjek non-ASCII dienkode RFC 2047 | `TestEnvelopeBytes`, `TestEnvelopeRejectsHeaderInjection` |
| SMTP: AUTH PLAIN, dan menolak mengirim jika STARTTLS diwajibkan tetapi tidak tersedia | `TestSMTPTransport`, `TestSMTPRequiresStartTLSWhenConfigured` |
| Semua template ter-render tanpa field yang hilang | `TestEmailTemplatesRender` |

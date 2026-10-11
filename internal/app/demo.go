package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"math"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/auth"
	"desakita/internal/platform/config"
	"desakita/internal/platform/upload"
	"desakita/internal/tourpackage"
	"desakita/internal/village"
)

// Demo data (SPEC §17) fills a development database with verified tourism villages in the
// launch cities, so search, filters, the map and booking can be tried at once. The villages
// are fictional and every description says so; phone numbers are unassigned placeholders.
// It never runs in production.

// DemoManagerEmail and DemoPassword sign in as the manager of every demo village.
const (
	DemoManagerEmail = "pengelola.demo@desakita.local"
	DemoPassword     = "demo12345"
)

type demoPackage struct {
	name, description   string
	price, days, nights int
	quota               int
	activities          []string
	sky, field          color.RGBA
	caption             string
}

type demoVillage struct {
	name, district, address, description string
	lat, lng                             string
	sky, field                           color.RGBA
	caption                              string
	packages                             []demoPackage
}

var (
	skyBlue  = color.RGBA{130, 185, 230, 255}
	dusk     = color.RGBA{240, 190, 150, 255}
	haze     = color.RGBA{205, 215, 225, 255}
	green    = color.RGBA{60, 130, 70, 255}
	tea      = color.RGBA{90, 150, 80, 255}
	sea      = color.RGBA{40, 120, 170, 255}
	roof     = color.RGBA{170, 90, 60, 255}
	volcanic = color.RGBA{110, 105, 100, 255}
)

const demoNote = "Data contoh untuk mencoba DesaKita, bukan desa sungguhan."

var demoVillages = []demoVillage{
	{
		name: "Kampung Wisata Lembang Asri", district: "Lembang", address: "Jalan Kebun Stroberi, Lembang, Bandung Barat",
		description: demoNote + " Kampung di dataran tinggi utara Bandung dengan kebun stroberi, peternakan sapi perah, dan hutan pinus.",
		lat:         "-6.8117", lng: "107.6175", sky: skyBlue, field: green, caption: "Kebun stroberi di lereng Lembang",
		packages: []demoPackage{
			{"Petik Stroberi dan Memerah Susu Sapi", "Memetik stroberi di kebun warga, belajar memerah susu, lalu membuat yoghurt sederhana. Termasuk camilan dan susu segar.",
				125000, 1, 0, 30, []string{"pertanian", "edukasi"}, skyBlue, green, "Wisatawan memetik stroberi"},
			{"Kemah Hutan Pinus Dua Hari", "Berkemah di hutan pinus bersama pemandu lokal, menyusuri jalur air terjun, dan api unggun malam hari. Tenda dan makan disediakan.",
				350000, 2, 1, 20, []string{"alam"}, haze, tea, "Tenda di antara pohon pinus"},
		},
	},
	{
		name: "Desa Wisata Ciwidey Hijau", district: "Ciwidey", address: "Kampung Kebun Teh, Ciwidey, Kabupaten Bandung",
		description: demoNote + " Desa di selatan Bandung yang dikelilingi kebun teh dan danau vulkanik.",
		lat:         "-7.0995", lng: "107.4430", sky: haze, field: tea, caption: "Hamparan kebun teh Ciwidey",
		packages: []demoPackage{
			{"Jelajah Kebun Teh dan Memetik Pucuk", "Berjalan di kebun teh bersama pemetik, belajar memetik pucuk, dan mencicipi teh di rumah warga.",
				175000, 1, 0, 25, []string{"alam", "pertanian"}, haze, tea, "Pemetik teh di pagi hari"},
		},
	},
	{
		name: "Desa Wisata Lereng Merapi", district: "Cangkringan", address: "Dusun Kaliadem, Cangkringan, Sleman",
		description: demoNote + " Desa di lereng Gunung Merapi. Warga memandu wisata jip, cerita erupsi, dan kehidupan petani salak.",
		lat:         "-7.6110", lng: "110.4430", sky: haze, field: volcanic, caption: "Pemandangan Gunung Merapi dari desa",
		packages: []demoPackage{
			{"Jip Lava Tour dan Cerita Warga", "Menyusuri jalur lava dengan jip bersama sopir warga, mampir ke museum rumah warga, dan mendengar kisah erupsi 2010.",
				250000, 1, 0, 24, []string{"alam", "edukasi"}, haze, volcanic, "Jip melintasi jalur pasir Merapi"},
			{"Menginap di Rumah Warga Merapi", "Tinggal semalam bersama keluarga petani, ikut memasak makanan desa, dan menikmati matahari terbit di gardu pandang.",
				300000, 2, 1, 12, []string{"budaya", "kuliner"}, dusk, green, "Sarapan bersama keluarga tuan rumah"},
		},
	},
	{
		name: "Kampung Batik Imogiri", district: "Imogiri", address: "Dusun Giriloyo, Imogiri, Bantul",
		description: demoNote + " Kampung pembatik di selatan Yogyakarta. Ibu-ibu perajin mengajarkan batik tulis dengan pewarna alami.",
		lat:         "-7.9220", lng: "110.3930", sky: dusk, field: roof, caption: "Perajin batik tulis di beranda rumah",
		packages: []demoPackage{
			{"Belajar Membatik Tulis", "Membuat sapu tangan batik tulis dari mencanting sampai mewarnai dengan pewarna alami. Hasil karya dibawa pulang.",
				150000, 1, 0, 20, []string{"kerajinan", "budaya"}, dusk, roof, "Mencanting motif batik"},
			{"Gamelan dan Kuliner Bantul", "Belajar menabuh gamelan di pendopo desa lalu memasak sayur lombok ijo dan mencicipi jajanan pasar.",
				120000, 1, 0, 25, []string{"budaya", "kuliner"}, dusk, green, "Latihan gamelan di pendopo"},
		},
	},
	{
		name: "Kampung Heritage Bubutan", district: "Bubutan", address: "Gang Kampung Lama, Bubutan, Surabaya",
		description: demoNote + " Kampung kota di jantung Surabaya dengan rumah-rumah lama, makam tokoh, dan cerita perjuangan 1945.",
		lat:         "-7.2460", lng: "112.7350", sky: skyBlue, field: roof, caption: "Gang kampung lama di Surabaya",
		packages: []demoPackage{
			{"Jalan Kaki Sejarah Kampung Kota", "Berjalan kaki dua jam bersama pemuda kampung menyusuri rumah lama, langgar tua, dan kisah pertempuran Surabaya.",
				75000, 1, 0, 30, []string{"budaya", "edukasi"}, skyBlue, roof, "Rombongan menyusuri gang bersejarah"},
			{"Kelas Masak Rujak Cingur", "Memasak rujak cingur dan lontong balap bersama warga, lengkap dengan cerita asal-usul kuliner Surabaya.",
				100000, 1, 0, 16, []string{"kuliner"}, dusk, roof, "Menyiapkan bumbu petis rujak cingur"},
		},
	},
	{
		name: "Kampung Nelayan Bulak Lestari", district: "Bulak", address: "Pesisir Bulak, Surabaya",
		description: demoNote + " Kampung nelayan di pesisir utara Surabaya. Warga mengolah ikan asap dan kerupuk ikan.",
		lat:         "-7.2290", lng: "112.7900", sky: skyBlue, field: sea, caption: "Perahu nelayan di pesisir Bulak",
		packages: []demoPackage{
			{"Melaut Pagi dan Olahan Ikan Asap", "Ikut perahu nelayan saat subuh, kembali ke kampung, lalu belajar mengasap ikan dan membuat kerupuk.",
				125000, 1, 0, 12, []string{"alam", "kuliner", "edukasi"}, dusk, sea, "Matahari terbit dari perahu nelayan"},
		},
	},
	{
		name: "Kampung Budaya Betawi Jagakarsa", district: "Jagakarsa", address: "Jalan Setu, Jagakarsa, Jakarta Selatan",
		description: demoNote + " Kampung di selatan Jakarta yang menjaga rumah adat, kesenian, dan kuliner Betawi di tepi setu.",
		lat:         "-6.3410", lng: "106.8250", sky: skyBlue, field: green, caption: "Rumah adat Betawi di tepi setu",
		packages: []demoPackage{
			{"Belajar Tari dan Musik Betawi", "Belajar dasar tari Yapong dan menabuh gambang kromong bersama sanggar kampung.",
				100000, 1, 0, 25, []string{"budaya"}, skyBlue, roof, "Latihan tari di sanggar"},
			{"Masak Kerak Telor dan Bir Pletok", "Membuat kerak telor di atas tungku arang dan meracik bir pletok, minuman rempah khas Betawi.",
				90000, 1, 0, 20, []string{"kuliner"}, dusk, roof, "Kerak telor di atas tungku arang"},
		},
	},
	{
		name: "Desa Wisata Bahari Seribu", district: "Kepulauan Seribu Selatan", address: "Pulau kecil di Kepulauan Seribu Selatan",
		description: demoNote + " Desa pulau dua jam dengan kapal dari Jakarta. Pantai pasir putih, terumbu karang, dan hutan mangrove.",
		lat:         "-5.8590", lng: "106.6200", sky: skyBlue, field: sea, caption: "Pantai pasir putih Kepulauan Seribu",
		packages: []demoPackage{
			{"Snorkeling dan Tanam Mangrove", "Dua hari di pulau: snorkeling di terumbu karang, menanam bibit mangrove, dan menginap di homestay warga. Termasuk makan.",
				450000, 2, 1, 16, []string{"alam", "edukasi"}, skyBlue, sea, "Snorkeling di perairan jernih"},
		},
	},
}

// landscapeJPEG draws a soft illustrated landscape (gradient sky, sun, two hill layers and
// a foreground) so demo pages have pleasant placeholder photos. seed varies the shapes.
func landscapeJPEG(sky, land color.RGBA, seed int) (*bytes.Reader, error) {
	const w, h = 960, 640
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	mix := func(a, b color.RGBA, t float64) color.RGBA {
		f := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
		return color.RGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), 255}
	}
	white := color.RGBA{255, 255, 255, 255}
	far := mix(land, sky, 0.55)
	near := land
	front := mix(land, color.RGBA{20, 30, 20, 255}, 0.25)
	ph := float64(seed%7) * 0.9
	water := land.B > land.G
	sunX, sunY, sunR := float64(w)*(0.2+0.12*float64(seed%5)), float64(h)*0.3, 46.0
	for y := 0; y < h; y++ {
		fy := float64(y)
		for x := 0; x < w; x++ {
			fx := float64(x)
			c := mix(mix(sky, white, 0.35), sky, fy/float64(h)) // lighter towards the horizon
			if dx, dy := fx-sunX, fy-sunY; dx*dx+dy*dy < sunR*sunR {
				c = mix(c, color.RGBA{255, 244, 214, 255}, 0.85)
			}
			farLine := float64(h)*0.52 + 34*math.Sin(fx/150+ph) + 18*math.Sin(fx/53+ph*2)
			nearLine := float64(h)*0.64 + 42*math.Sin(fx/210+ph*1.7) + 12*math.Sin(fx/41+ph)
			frontLine := float64(h)*0.82 + 16*math.Sin(fx/120+ph*0.5)
			if water { // a calm sea: flat horizon, gentle swell
				nearLine = float64(h)*0.6 + 3*math.Sin(fx/30+fy/9)
				frontLine = float64(h)*0.86 + 5*math.Sin(fx/70+ph)
			}
			switch {
			case fy > frontLine:
				c = front
			case fy > nearLine:
				c = mix(near, front, (fy-nearLine)/(frontLine-nearLine+1)*0.5)
			case fy > farLine:
				c = far
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return bytes.NewReader(buf.Bytes()), nil
}

// SeedDemo adds the demo villages that don't exist yet. It is idempotent and refuses to
// run when APP_ENV=production.
func SeedDemo(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, logger *slog.Logger) (added int, err error) {
	if cfg.IsProduction() {
		return 0, errors.New("seed-demo is for development only (APP_ENV=production)")
	}
	authSvc, err := auth.NewService(pool)
	if err != nil {
		return 0, err
	}
	store, err := upload.NewLocalStore(cfg.UploadDir)
	if err != nil {
		return 0, err
	}
	villages := village.NewService(pool, store, logger)
	packages := tourpackage.NewService(pool, villages, store, logger)

	var managerID int64
	err = pool.QueryRow(ctx, "SELECT id FROM users WHERE email = $1", DemoManagerEmail).Scan(&managerID)
	if errors.Is(err, pgx.ErrNoRows) {
		u, err := authSvc.Register(ctx, auth.RegisterInput{Name: "Pengelola Contoh", Email: DemoManagerEmail, Password: DemoPassword})
		if err != nil {
			return 0, fmt.Errorf("demo manager: %w", err)
		}
		managerID = u.ID
	} else if err != nil {
		return 0, err
	}

	activityIDs := map[string]string{}
	rows, err := pool.Query(ctx, "SELECT slug, id FROM activity_types")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var slug string
		var id int64
		if err := rows.Scan(&slug, &id); err != nil {
			return 0, err
		}
		activityIDs[slug] = strconv.FormatInt(id, 10)
	}
	rows.Close()
	if len(activityIDs) == 0 {
		return 0, errors.New("no activity types: run `desakita seed` first")
	}

	for i, dv := range demoVillages {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM villages WHERE slug = $1)", village.Slugify(dv.name)).Scan(&exists); err != nil {
			return added, err
		}
		if exists {
			continue
		}
		var district int64
		if err := pool.QueryRow(ctx, "SELECT id FROM regions WHERE kind = 'district' AND name = $1", dv.district).Scan(&district); err != nil {
			return added, fmt.Errorf("district %s (run `desakita migrate up`): %w", dv.district, err)
		}
		vid, err := villages.Create(ctx, managerID, village.ProfileInput{
			Name: dv.name, DistrictID: strconv.FormatInt(district, 10), Address: dv.address, Description: dv.description,
			ContactPhone: fmt.Sprintf("0812-0000-%04d", i+1), Lat: dv.lat, Lng: dv.lng,
		})
		if err != nil {
			return added, fmt.Errorf("village %s: %w", dv.name, err)
		}
		// Demo villages skip the admin queue; real ones are verified in /admin (audited).
		if _, err := pool.Exec(ctx, "UPDATE villages SET status = 'verified', verified_at = now() WHERE id = $1", vid); err != nil {
			return added, err
		}
		photo, err := landscapeJPEG(dv.sky, dv.field, i)
		if err != nil {
			return added, err
		}
		if err := villages.AddPhoto(ctx, managerID, vid, photo, true, dv.caption); err != nil {
			return added, fmt.Errorf("village photo: %w", err)
		}
		for j, dp := range dv.packages {
			var types []string
			for _, a := range dp.activities {
				types = append(types, activityIDs[a])
			}
			pid, err := packages.Create(ctx, vid, tourpackage.Input{
				Name: dp.name, Description: dp.description, Price: strconv.Itoa(dp.price),
				DurationDays: strconv.Itoa(dp.days), DurationNights: strconv.Itoa(dp.nights), DefaultQuota: strconv.Itoa(dp.quota),
				ActivityTypeIDs: types,
			})
			if err != nil {
				return added, fmt.Errorf("package %s: %w", dp.name, err)
			}
			photo, err := landscapeJPEG(dp.sky, dp.field, i*3+j+1)
			if err != nil {
				return added, err
			}
			if err := packages.AddPhoto(ctx, managerID, pid, photo, true, dp.caption); err != nil {
				return added, fmt.Errorf("package photo: %w", err)
			}
		}
		added++
	}
	return added, nil
}

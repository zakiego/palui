package eval

// Phrases is reminders/eval/phrases.ts: 60 phrases, half Indonesian, half
// English, against a fixed "now" of Tuesday 2026-11-10 10:00 in Asia/Makassar.
// LocalDateTime is the expected first fire in wall-clock time; RRule the
// expected rule ("" for a one-off). Conventions follow the parse prompt: a date
// with no time is 09:00, pagi 08:00, siang 12:00, sore 16:00, malam 20:00, a
// bare hour 1-6 is PM, and a time already passed today with no day given
// means tomorrow.

const (
	EvalTZ       = "Asia/Makassar"
	EvalNowLocal = "2026-11-10T10:00"
)

type Phrase struct {
	Lang          string
	Text          string
	LocalDateTime string
	RRule         string
}

var Phrases = []Phrase{
	{"id", "ingetin bayar listrik besok jam 9", "2026-11-11T09:00", ""},
	{"id", "ingatkan aku minum obat 2 jam lagi", "2026-11-10T12:00", ""},
	{"id", "nanti sore jam 4 telepon ibu", "2026-11-10T16:00", ""},
	{"id", "lusa jam 10 pagi meeting sama klien", "2026-11-12T10:00", ""},
	{"id", "tiap Senin jam 8 kirim laporan mingguan", "2026-11-16T08:00", "FREQ=WEEKLY;BYDAY=MO"},
	{"id", "setiap hari jam 7 pagi olahraga", "2026-11-11T07:00", "FREQ=DAILY"},
	{"id", "ingetin jemput anak jam 3 sore", "2026-11-10T15:00", ""},
	{"id", "30 menit lagi angkat jemuran", "2026-11-10T10:30", ""},
	{"id", "tanggal 25 bayar cicilan motor", "2026-11-25T09:00", ""},
	{"id", "tiap tanggal 1 bayar kos", "2026-12-01T09:00", "FREQ=MONTHLY;BYMONTHDAY=1"},
	{"id", "Jumat ini jam 11.30 berangkat jumatan", "2026-11-13T11:30", ""},
	{"id", "jam 9 malam ini cek oven", "2026-11-10T21:00", ""},
	{"id", "besok pagi beli sayur di pasar", "2026-11-11T08:00", ""},
	{"id", "setiap hari kerja jam 9 standup", "2026-11-11T09:00", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
	{"id", "jumat terakhir tiap bulan jam 5 sore rapat evaluasi", "2026-11-27T17:00", "FREQ=MONTHLY;BYDAY=-1FR"},
	{"id", "ingetin setengah jam lagi matiin kompor", "2026-11-10T10:30", ""},
	{"id", "minggu depan hari Senin jam 10 servis motor", "2026-11-16T10:00", ""},
	{"id", "tiap 2 minggu sekali hari Sabtu jam 9 potong rambut", "2026-11-14T09:00", "FREQ=WEEKLY;INTERVAL=2;BYDAY=SA"},
	{"id", "akhir bulan bayar tagihan internet", "2026-11-30T09:00", ""},
	{"id", "tiap akhir bulan jam 8 malam rekap keuangan", "2026-11-30T20:00", "FREQ=MONTHLY;BYMONTHDAY=-1"},
	{"id", "tiap tahun tanggal 17 Agustus pasang bendera", "2027-08-17T09:00", "FREQ=YEARLY;BYMONTH=8;BYMONTHDAY=17"},
	{"id", "jam 9 ingetin cek email kantor", "2026-11-11T09:00", ""},
	{"id", "1 jam 15 menit lagi keluarin ayam dari freezer", "2026-11-10T11:15", ""},
	{"id", "Kamis jam 2 siang ke dokter gigi", "2026-11-12T14:00", ""},
	{"id", "tiap Selasa dan Kamis jam 6 sore les gitar", "2026-11-10T18:00", "FREQ=WEEKLY;BYDAY=TU,TH"},
	{"id", "25 Desember jam 7 malam makan malam keluarga", "2026-12-25T19:00", ""},
	{"id", "tiap hari jam 8 pagi dan jam 8 malam minum vitamin", "2026-11-10T20:00", "FREQ=DAILY;BYHOUR=8,20;BYMINUTE=0"},
	{"id", "nanti malam kunci pintu gerbang", "2026-11-10T20:00", ""},
	{"id", "tiap 3 jam minum air putih", "2026-11-10T13:00", "FREQ=HOURLY;INTERVAL=3"},
	{"id", "hari Minggu jam 6 pagi lari di stadion", "2026-11-15T06:00", ""},
	{"en", "remind me tomorrow at 9am to pay the electricity bill", "2026-11-11T09:00", ""},
	{"en", "remind me every Monday at 8am to send the report", "2026-11-16T08:00", "FREQ=WEEKLY;BYDAY=MO"},
	{"en", "in 2 hours call mom", "2026-11-10T12:00", ""},
	{"en", "in 45 minutes check the laundry", "2026-11-10T10:45", ""},
	{"en", "on Friday at 3pm submit the timesheet", "2026-11-13T15:00", ""},
	{"en", "every day at 10pm take my medicine", "2026-11-10T22:00", "FREQ=DAILY"},
	{"en", "last Friday of every month at 4pm team retro", "2026-11-27T16:00", "FREQ=MONTHLY;BYDAY=-1FR"},
	{"en", "the day after tomorrow at noon lunch with Andi", "2026-11-12T12:00", ""},
	{"en", "December 1st renew the domain", "2026-12-01T09:00", ""},
	{"en", "every weekday at 8:30am check the dashboard", "2026-11-11T08:30", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
	{"en", "tonight at 8 water the plants", "2026-11-10T20:00", ""},
	{"en", "this afternoon at 2:30 pick up the package", "2026-11-10T14:30", ""},
	{"en", "every first Monday of the month at 9 budget review", "2026-12-07T09:00", "FREQ=MONTHLY;BYDAY=1MO"},
	{"en", "every 2 weeks on Thursday at 7pm book club", "2026-11-12T19:00", "FREQ=WEEKLY;INTERVAL=2;BYDAY=TH"},
	{"en", "on the 15th pay the credit card", "2026-11-15T09:00", ""},
	{"en", "every year on March 3 at 9am mom's birthday", "2027-03-03T09:00", "FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=3"},
	{"en", "at 5 remind me to leave the office", "2026-11-10T17:00", ""},
	{"en", "next Monday at 10 dentist appointment", "2026-11-16T10:00", ""},
	{"en", "tomorrow morning go to the gym", "2026-11-11T08:00", ""},
	{"en", "every Saturday and Sunday at 7am long run", "2026-11-14T07:00", "FREQ=WEEKLY;BYDAY=SA,SU"},
	{"en", "in 90 minutes join the webinar", "2026-11-10T11:30", ""},
	{"en", "on New Year's Eve at 11pm call grandma", "2026-12-31T23:00", ""},
	{"en", "every month on the 31st at 9am pay rent", "2026-12-31T09:00", "FREQ=MONTHLY;BYMONTHDAY=31"},
	{"en", "every evening at 9 write in my journal", "2026-11-10T21:00", "FREQ=DAILY"},
	{"en", "remind me at 7 tomorrow evening to call Budi", "2026-11-11T19:00", ""},
	{"en", "on the last day of the month send the invoices", "2026-11-30T09:00", ""},
	{"en", "Thursday next week at 11am quarterly review", "2026-11-19T11:00", ""},
	{"en", "every Tuesday at 9am water the garden", "2026-11-17T09:00", "FREQ=WEEKLY;BYDAY=TU"},
	{"en", "in 3 days at 6pm return the library book", "2026-11-13T18:00", ""},
	{"en", "every morning at 6:30 meditate", "2026-11-11T06:30", "FREQ=DAILY"},
}

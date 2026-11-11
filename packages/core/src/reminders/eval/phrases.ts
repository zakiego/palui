// The reminder parse eval set (PLAN 6, 17): 60 phrases, half Indonesian, half
// English, against a fixed "now" of Tuesday 2026-11-10 10:00 in Asia/Makassar.
//
// `localDateTime` is the expected first fire in wall-clock time; `rrule` the
// expected rule (null for a one-off). A result matches when the fire instant is the
// same and the rule is equivalent (same normalized body, or the same next
// occurrences). Conventions follow the parse prompt: a date with no time is 09:00,
// pagi 08:00, siang 12:00, sore 16:00, malam 20:00, a bare hour 1-6 is PM, and a
// time already passed today with no day given means tomorrow.

export const EVAL_TZ = "Asia/Makassar"
export const EVAL_NOW_LOCAL = "2026-11-10T10:00"

export type EvalPhrase = { lang: "id" | "en"; text: string; localDateTime: string; rrule: string | null }

export const PHRASES: EvalPhrase[] = [
  // --- Indonesian --------------------------------------------------------------------
  { lang: "id", text: "ingetin bayar listrik besok jam 9", localDateTime: "2026-11-11T09:00", rrule: null },
  { lang: "id", text: "ingatkan aku minum obat 2 jam lagi", localDateTime: "2026-11-10T12:00", rrule: null },
  { lang: "id", text: "nanti sore jam 4 telepon ibu", localDateTime: "2026-11-10T16:00", rrule: null },
  { lang: "id", text: "lusa jam 10 pagi meeting sama klien", localDateTime: "2026-11-12T10:00", rrule: null },
  { lang: "id", text: "tiap Senin jam 8 kirim laporan mingguan", localDateTime: "2026-11-16T08:00", rrule: "FREQ=WEEKLY;BYDAY=MO" },
  { lang: "id", text: "setiap hari jam 7 pagi olahraga", localDateTime: "2026-11-11T07:00", rrule: "FREQ=DAILY" },
  { lang: "id", text: "ingetin jemput anak jam 3 sore", localDateTime: "2026-11-10T15:00", rrule: null },
  { lang: "id", text: "30 menit lagi angkat jemuran", localDateTime: "2026-11-10T10:30", rrule: null },
  { lang: "id", text: "tanggal 25 bayar cicilan motor", localDateTime: "2026-11-25T09:00", rrule: null },
  { lang: "id", text: "tiap tanggal 1 bayar kos", localDateTime: "2026-12-01T09:00", rrule: "FREQ=MONTHLY;BYMONTHDAY=1" },
  { lang: "id", text: "Jumat ini jam 11.30 berangkat jumatan", localDateTime: "2026-11-13T11:30", rrule: null },
  { lang: "id", text: "jam 9 malam ini cek oven", localDateTime: "2026-11-10T21:00", rrule: null },
  { lang: "id", text: "besok pagi beli sayur di pasar", localDateTime: "2026-11-11T08:00", rrule: null },
  { lang: "id", text: "setiap hari kerja jam 9 standup", localDateTime: "2026-11-11T09:00", rrule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" },
  { lang: "id", text: "jumat terakhir tiap bulan jam 5 sore rapat evaluasi", localDateTime: "2026-11-27T17:00", rrule: "FREQ=MONTHLY;BYDAY=-1FR" },
  { lang: "id", text: "ingetin setengah jam lagi matiin kompor", localDateTime: "2026-11-10T10:30", rrule: null },
  { lang: "id", text: "minggu depan hari Senin jam 10 servis motor", localDateTime: "2026-11-16T10:00", rrule: null },
  { lang: "id", text: "tiap 2 minggu sekali hari Sabtu jam 9 potong rambut", localDateTime: "2026-11-14T09:00", rrule: "FREQ=WEEKLY;INTERVAL=2;BYDAY=SA" },
  { lang: "id", text: "akhir bulan bayar tagihan internet", localDateTime: "2026-11-30T09:00", rrule: null },
  { lang: "id", text: "tiap akhir bulan jam 8 malam rekap keuangan", localDateTime: "2026-11-30T20:00", rrule: "FREQ=MONTHLY;BYMONTHDAY=-1" },
  { lang: "id", text: "tiap tahun tanggal 17 Agustus pasang bendera", localDateTime: "2027-08-17T09:00", rrule: "FREQ=YEARLY;BYMONTH=8;BYMONTHDAY=17" },
  { lang: "id", text: "jam 9 ingetin cek email kantor", localDateTime: "2026-11-11T09:00", rrule: null },
  { lang: "id", text: "1 jam 15 menit lagi keluarin ayam dari freezer", localDateTime: "2026-11-10T11:15", rrule: null },
  { lang: "id", text: "Kamis jam 2 siang ke dokter gigi", localDateTime: "2026-11-12T14:00", rrule: null },
  { lang: "id", text: "tiap Selasa dan Kamis jam 6 sore les gitar", localDateTime: "2026-11-10T18:00", rrule: "FREQ=WEEKLY;BYDAY=TU,TH" },
  { lang: "id", text: "25 Desember jam 7 malam makan malam keluarga", localDateTime: "2026-12-25T19:00", rrule: null },
  { lang: "id", text: "tiap hari jam 8 pagi dan jam 8 malam minum vitamin", localDateTime: "2026-11-10T20:00", rrule: "FREQ=DAILY;BYHOUR=8,20;BYMINUTE=0" },
  { lang: "id", text: "nanti malam kunci pintu gerbang", localDateTime: "2026-11-10T20:00", rrule: null },
  { lang: "id", text: "tiap 3 jam minum air putih", localDateTime: "2026-11-10T13:00", rrule: "FREQ=HOURLY;INTERVAL=3" },
  { lang: "id", text: "hari Minggu jam 6 pagi lari di stadion", localDateTime: "2026-11-15T06:00", rrule: null },

  // --- English -----------------------------------------------------------------------
  { lang: "en", text: "remind me tomorrow at 9am to pay the electricity bill", localDateTime: "2026-11-11T09:00", rrule: null },
  { lang: "en", text: "remind me every Monday at 8am to send the report", localDateTime: "2026-11-16T08:00", rrule: "FREQ=WEEKLY;BYDAY=MO" },
  { lang: "en", text: "in 2 hours call mom", localDateTime: "2026-11-10T12:00", rrule: null },
  { lang: "en", text: "in 45 minutes check the laundry", localDateTime: "2026-11-10T10:45", rrule: null },
  { lang: "en", text: "on Friday at 3pm submit the timesheet", localDateTime: "2026-11-13T15:00", rrule: null },
  { lang: "en", text: "every day at 10pm take my medicine", localDateTime: "2026-11-10T22:00", rrule: "FREQ=DAILY" },
  { lang: "en", text: "last Friday of every month at 4pm team retro", localDateTime: "2026-11-27T16:00", rrule: "FREQ=MONTHLY;BYDAY=-1FR" },
  { lang: "en", text: "the day after tomorrow at noon lunch with Andi", localDateTime: "2026-11-12T12:00", rrule: null },
  { lang: "en", text: "December 1st renew the domain", localDateTime: "2026-12-01T09:00", rrule: null },
  { lang: "en", text: "every weekday at 8:30am check the dashboard", localDateTime: "2026-11-11T08:30", rrule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" },
  { lang: "en", text: "tonight at 8 water the plants", localDateTime: "2026-11-10T20:00", rrule: null },
  { lang: "en", text: "this afternoon at 2:30 pick up the package", localDateTime: "2026-11-10T14:30", rrule: null },
  { lang: "en", text: "every first Monday of the month at 9 budget review", localDateTime: "2026-12-07T09:00", rrule: "FREQ=MONTHLY;BYDAY=1MO" },
  { lang: "en", text: "every 2 weeks on Thursday at 7pm book club", localDateTime: "2026-11-12T19:00", rrule: "FREQ=WEEKLY;INTERVAL=2;BYDAY=TH" },
  { lang: "en", text: "on the 15th pay the credit card", localDateTime: "2026-11-15T09:00", rrule: null },
  { lang: "en", text: "every year on March 3 at 9am mom's birthday", localDateTime: "2027-03-03T09:00", rrule: "FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=3" },
  { lang: "en", text: "at 5 remind me to leave the office", localDateTime: "2026-11-10T17:00", rrule: null },
  { lang: "en", text: "next Monday at 10 dentist appointment", localDateTime: "2026-11-16T10:00", rrule: null },
  { lang: "en", text: "tomorrow morning go to the gym", localDateTime: "2026-11-11T08:00", rrule: null },
  { lang: "en", text: "every Saturday and Sunday at 7am long run", localDateTime: "2026-11-14T07:00", rrule: "FREQ=WEEKLY;BYDAY=SA,SU" },
  { lang: "en", text: "in 90 minutes join the webinar", localDateTime: "2026-11-10T11:30", rrule: null },
  { lang: "en", text: "on New Year's Eve at 11pm call grandma", localDateTime: "2026-12-31T23:00", rrule: null },
  { lang: "en", text: "every month on the 31st at 9am pay rent", localDateTime: "2026-12-31T09:00", rrule: "FREQ=MONTHLY;BYMONTHDAY=31" },
  { lang: "en", text: "every evening at 9 write in my journal", localDateTime: "2026-11-10T21:00", rrule: "FREQ=DAILY" },
  { lang: "en", text: "remind me at 7 tomorrow evening to call Budi", localDateTime: "2026-11-11T19:00", rrule: null },
  { lang: "en", text: "on the last day of the month send the invoices", localDateTime: "2026-11-30T09:00", rrule: null },
  { lang: "en", text: "Thursday next week at 11am quarterly review", localDateTime: "2026-11-19T11:00", rrule: null },
  { lang: "en", text: "every Tuesday at 9am water the garden", localDateTime: "2026-11-17T09:00", rrule: "FREQ=WEEKLY;BYDAY=TU" },
  { lang: "en", text: "in 3 days at 6pm return the library book", localDateTime: "2026-11-13T18:00", rrule: null },
  { lang: "en", text: "every morning at 6:30 meditate", localDateTime: "2026-11-11T06:30", rrule: "FREQ=DAILY" },
]

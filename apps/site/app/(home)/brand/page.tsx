import { Download } from "lucide-react"
import type { Metadata, Viewport } from "next"
import { appName, siteImage } from "@/lib/shared"
import "@/components/landing/brand.css"

const title = "Brand"
const description = "Palui's face and colors, ready to use. Download a profile picture for the WhatsApp or Telegram account your Palui runs on."

export const metadata: Metadata = {
  title,
  description,
  alternates: { canonical: "/brand" },
  // Page-level openGraph replaces the layout's, so it's complete here.
  openGraph: { type: "website", siteName: appName, locale: "en_US", url: "/brand", title: `${title} · ${appName}`, description, images: [siteImage] },
  twitter: { card: "summary_large_image", title: `${title} · ${appName}`, description, images: [siteImage] },
}

export const viewport: Viewport = { themeColor: "#0a0a0a", colorScheme: "dark" }

// Every file here is written by scripts/brand-icons.mjs into public/brand.
const avatars = [
  { file: "palui-avatar-head", name: "Head", note: "The mascot. Reads best on a phone." },
  { file: "palui-avatar-dark", name: "Face, dark", note: "Flat, white on black." },
  { file: "palui-avatar-light", name: "Face, light", note: "Flat, black on white." },
]

const marks = [
  { file: "palui-mark-on-dark", name: "On dark", bg: "#0a0a0a" },
  { file: "palui-mark-on-light", name: "On light", bg: "#ffffff" },
]

const colors = [
  { name: "Ink", hex: "#0a0a0a", use: "Page" },
  { name: "Surface", hex: "#111111", use: "Cards" },
  { name: "Line", hex: "#272727", use: "Borders" },
  { name: "Dim", hex: "#a1a1a1", use: "Muted text" },
  { name: "Text", hex: "#ededed", use: "Body text" },
  { name: "White", hex: "#ffffff", use: "The one accent" },
]

export default function BrandPage() {
  return (
    <main className="br-main">
      <div className="br-intro">
        <h1 className="br-h1">Brand</h1>
        <p className="br-lede">{description}</p>
      </div>

      <section className="br-section" aria-labelledby="avatar">
        <h2 id="avatar" className="br-h2">
          Profile picture
        </h2>
        <p className="br-text">Square, 1024 × 1024. WhatsApp and Telegram crop it to a circle, and everything stays inside it.</p>
        <ul className="br-avatars">
          {avatars.map((a) => (
            <li key={a.file} className="br-card">
              <div className="br-preview">
                <img src={`/brand/${a.file}.png`} alt={`Palui profile picture, ${a.name.toLowerCase()}`} width={160} height={160} className="br-circle" />
                {/* The size it shows at in a chat list. */}
                <img src={`/brand/${a.file}.png`} alt="" width={40} height={40} className="br-circle br-circle-sm" />
              </div>
              <div className="br-card-body">
                <h3 className="br-h3">{a.name}</h3>
                <p className="br-note">{a.note}</p>
                <div className="br-actions">
                  <a href={`/brand/${a.file}.png`} download className="br-btn">
                    <Download aria-hidden="true" />
                    PNG
                  </a>
                  <a href={`/brand/${a.file}.svg`} download className="br-btn br-btn-quiet">
                    SVG
                  </a>
                </div>
              </div>
            </li>
          ))}
        </ul>
        <ol className="br-steps">
          <li>
            <span className="br-step-label">WhatsApp</span> On the phone that holds Palui's number, open Settings, tap the photo, then Edit and pick the PNG.
          </li>
          <li>
            <span className="br-step-label">Telegram</span> Message <a href="https://t.me/BotFather">@BotFather</a>, send <code>/setuserpic</code>, choose your bot, then send the PNG as a photo.
          </li>
        </ol>
      </section>

      <section className="br-section" aria-labelledby="mark">
        <h2 id="mark" className="br-h2">
          Mark
        </h2>
        <p className="br-text">A kopiah over two dots and a smile. Use it as it is: don't recolor, stretch or rotate it, and don't add shadows or effects.</p>
        <ul className="br-marks">
          {marks.map((m) => (
            <li key={m.file} className="br-mark" style={{ background: m.bg }}>
              <img src={`/brand/${m.file}.svg`} alt={`Palui mark ${m.name.toLowerCase()}`} width={72} height={72} />
              <a href={`/brand/${m.file}.svg`} download className={m.bg === "#ffffff" ? "br-btn br-btn-quiet br-on-light" : "br-btn br-btn-quiet"}>
                <Download aria-hidden="true" />
                {m.name}, SVG
              </a>
            </li>
          ))}
        </ul>
      </section>

      <section className="br-section" aria-labelledby="colors">
        <h2 id="colors" className="br-h2">
          Colors
        </h2>
        <p className="br-text">Black and grays with no hue, and white as the only accent. No gradients.</p>
        <ul className="br-colors">
          {colors.map((c) => (
            <li key={c.hex} className="br-color">
              <span className="br-swatch" style={{ background: c.hex }} />
              <span className="br-color-name">{c.name}</span>
              <span className="br-color-hex">{c.hex}</span>
              <span className="br-color-use">{c.use}</span>
            </li>
          ))}
        </ul>
      </section>

      <section className="br-section" aria-labelledby="type">
        <h2 id="type" className="br-h2">
          Type
        </h2>
        <div className="br-type">
          <div className="br-type-row">
            <p className="br-specimen br-specimen-mono">An agent friend you host yourself</p>
            <p className="br-note">IBM Plex Mono, for headlines and small labels.</p>
          </div>
          <div className="br-type-row">
            <p className="br-specimen br-specimen-sans">Palui remembers what you tell it and reminds you on time.</p>
            <p className="br-note">IBM Plex Sans, for everything you read.</p>
          </div>
        </div>
      </section>

      <section className="br-section" aria-labelledby="name">
        <h2 id="name" className="br-h2">
          Name
        </h2>
        <ul className="br-rules">
          <li>Write "Palui Agent" the first time, then "Palui". Always a capital P.</li>
          <li>Lowercase "palui" only in code, package names and commands.</li>
          <li>Say it PAH-loo-ee. It's named after Si Palui, the Banjar folk comic hero. The mark is an original drawing, not the comic's art.</li>
        </ul>
      </section>
    </main>
  )
}

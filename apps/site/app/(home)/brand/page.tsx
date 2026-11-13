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

export const viewport: Viewport = { themeColor: "#08090a", colorScheme: "dark" }

// Every file here is written by scripts/brand-icons.mjs into public/brand,
// as palui-avatar-<design>-<theme>.png and .svg.
const avatars = [
  { design: "head", name: "Head", note: "The 3D head from the corner. Reads best in a chat list." },
  { design: "front", name: "Head, straight", note: "The same head, face on." },
  { design: "wave", name: "Waving", note: "Palui saying hi. A friendly first message." },
  { design: "bust", name: "Portrait", note: "Head and shoulders, cropped like a photo." },
  { design: "body", name: "Full body", note: "Palui standing. Best where the picture shows big." },
  { design: "tile", name: "App icon", note: "The flat mark on its tile." },
  { design: "face", name: "Face", note: "The flat face alone. Sharpest at tiny sizes." },
]
const themes = [
  { theme: "dark", name: "Dark" },
  { theme: "light", name: "Light" },
]

const marks = [
  { file: "palui-mark-on-dark", name: "On dark", bg: "#0a0a0a" },
  { file: "palui-mark-on-light", name: "On light", bg: "#ffffff" },
]

// The landing's palette, which is the dashboard's at night.
const colors = [
  { name: "Ink", hex: "#08090a", use: "Page" },
  { name: "Surface", hex: "#0f1011", use: "Cards" },
  { name: "Line", hex: "#23252a", use: "Borders" },
  { name: "Muted", hex: "#8a8f98", use: "Muted text" },
  { name: "Text", hex: "#f7f8f8", use: "Body text" },
  { name: "Indigo", hex: "#5e6ad2", use: "Focus and on" },
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
            <li key={a.design} className="br-card">
              <div className="br-card-body">
                <h3 className="br-h3">{a.name}</h3>
                <p className="br-note">{a.note}</p>
              </div>
              <div className="br-variants">
                {themes.map((t) => {
                  const file = `/brand/palui-avatar-${a.design}-${t.theme}`
                  return (
                    <div key={t.theme} className="br-variant">
                      <div className="br-preview">
                        <img src={`${file}.png`} alt={`Palui profile picture, ${a.name.toLowerCase()}, ${t.name.toLowerCase()}`} width={112} height={112} className="br-circle" />
                        {/* The size it shows at in a chat list. */}
                        <img src={`${file}.png`} alt="" width={32} height={32} className="br-circle br-circle-sm" />
                      </div>
                      <div className="br-actions">
                        <a href={`${file}.png`} download className="br-btn">
                          <Download aria-hidden="true" />
                          {t.name}
                        </a>
                        <a href={`${file}.svg`} download className="br-btn br-btn-quiet" aria-label={`${t.name} SVG`}>
                          SVG
                        </a>
                      </div>
                    </div>
                  )
                })}
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
            <p className="br-specimen br-specimen-head">An agent friend you host yourself</p>
            <p className="br-note">Inter, for headlines and everything you read.</p>
          </div>
          <div className="br-type-row">
            <p className="br-specimen br-specimen-mono">palui.zakiego.com #08090a</p>
            <p className="br-note">Geist Mono, for values a machine wrote.</p>
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

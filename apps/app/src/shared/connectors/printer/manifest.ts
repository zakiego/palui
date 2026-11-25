import { z } from "zod"
import { defineManifest } from "../define"

export const PRINTER_URL_PLACEHOLDER = "ipp://host.docker.internal:631/printers/My_Printer"

export const printerConfig = z.object({
  // The IPP endpoint of a print queue, as seen from the runtime. A CUPS queue on
  // the Mac works best: CUPS turns PDFs and photos into what the printer takes.
  url: z.url({ protocol: /^(ipps?|https?)$/ }),
})

export default defineManifest({
  id: "printer",
  kind: "tools",
  name: "Printer",
  icon: "printer",
  summary: "Print files from chat on your printer.",
  multiple: true,
  trust: "own",
  config: printerConfig,
  secrets: {},
  setup: [
    {
      field: "url",
      label: "Printer URL",
      help: "A CUPS queue on the Mac works best. lpstat -p lists the queue names.",
      command: "lpstat -p -d",
      placeholder: PRINTER_URL_PLACEHOLDER,
    },
  ],
  pauseNote: "While paused, Palui can't print. Jobs already sent keep printing.",
  removeWarning: "This removes the printer from Palui. The printer and its queue aren't touched.",
  docs: {
    commands: ["lpstat -p -d"],
    notes: [
      "Prints PDFs, JPEGs and PNGs sent in the chat or saved as attachments.",
      "Use the Mac's CUPS queue, not the printer's own address: many printers only take raster formats, and CUPS converts the file for them.",
      "Palui prints right away only when your own message asks for it (like 'print ini' or 'cetak'). A file sent with no caption, untrusted content or a scheduled job asks you first.",
    ],
  },
})

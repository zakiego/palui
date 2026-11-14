const re = /\p{Extended_Pictographic}/u
const ranges = []
let start = -1
for (let cp = 0; cp <= 0x110000; cp++) {
  const ok = cp < 0x110000 && !(cp >= 0xd800 && cp <= 0xdfff) && re.test(String.fromCodePoint(cp))
  if (ok && start < 0) start = cp
  if (!ok && start >= 0) { ranges.push([start, cp - 1]); start = -1 }
}
let out = "// Code generated from Node " + process.version + " /\\p{Extended_Pictographic}/u. DO NOT EDIT.\n\npackage ack\n\nvar extendedPictographic = [][2]rune{\n"
for (const [a, b] of ranges) out += `\t{0x${a.toString(16).toUpperCase()}, 0x${b.toString(16).toUpperCase()}},\n`
out += "}\n"
process.stdout.write(out)

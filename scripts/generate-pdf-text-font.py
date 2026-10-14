# Build-time only: uv run --with fonttools==4.66.1 scripts/generate-pdf-text-font.py
# Regenerates internal/pdf/text.ttf, PaperDotNet's OcrText.ttf byte for byte:
# one placeholder glyph with a one-em advance covers every Unicode CID of the
# invisible text layer of searchable PDFs.
from fontTools.fontBuilder import FontBuilder
from fontTools.pens.ttGlyphPen import TTGlyphPen
fb = FontBuilder(1000, isTTF=True)
fb.setupGlyphOrder([".notdef"])
fb.setupCharacterMap({})
p = TTGlyphPen(None)
p.moveTo((0, 0))
p.lineTo((1000, 0))
p.lineTo((1000, 1000))
p.lineTo((0, 1000))
p.closePath()
fb.setupGlyf({".notdef": p.glyph()})
fb.setupHorizontalMetrics({".notdef": (1000, 0)})
fb.setupHorizontalHeader(ascent=1000, descent=0)
fb.setupNameTable({
    "familyName": "PaperDotNet OCR Text",
    "styleName": "Regular",
    "uniqueFontIdentifier": "PaperDotNetOcrText",
    "fullName": "PaperDotNet OCR Text",
    "psName": "PaperDotNetOcrText",
})
fb.setupOS2(sTypoAscender=1000, sTypoDescender=0, usWinAscent=1000, usWinDescent=0)
fb.setupPost()
fb.setupMaxp()
fb.font["head"].created = fb.font["head"].modified = 3786912000
fb.save("internal/pdf/text.ttf")

"""Deterministic multilingual text corpus for tokenizer and embedding parity."""

import json
import random

SENTENCES = {
    "en": [
        "Electricity invoice for September was paid in full by bank transfer on 3 October.",
        "Please find attached the signed contract and the delivery note.",
        "The quarterly report shows a 4.5% increase in revenue compared to Q2 2025.",
        "Total amount due: EUR 1,234.56 — payable within 30 days.",
        "Reminder: your insurance policy #A-77812 renews on 2026-01-01.",
        "Don't forget the meeting at 10:30 a.m.; it's in room B-12.",
    ],
    "es": [
        "¿Dónde está la factura pagada de electricidad?",
        "El pago se realizó mediante transferencia bancaria el 3 de octubre.",
        "Adjuntamos el contrato firmado y el albarán de entrega.",
        "Señor Núñez, la reunión será el miércoles a las 9:00 h.",
        "Cobro duplicado: revisión urgente del importe de 89,90 €.",
    ],
    "de": [
        "Die Stromrechnung für September wurde vollständig per Überweisung bezahlt.",
        "Bitte senden Sie uns die unterschriebene Kündigungsbestätigung zurück.",
        "Größe, Straße, Fußgängerübergang und Ärztekammer sind schwierige Wörter.",
        "Gesamtbetrag inkl. 19 % MwSt.: 2.345,67 €",
    ],
    "fr": [
        "La facture d'électricité de septembre a été réglée par virement.",
        "Veuillez trouver ci-joint le bon de livraison signé.",
        "Où se trouve l'hôpital le plus proche ? C'est à côté de la gare.",
        "Œuvre, cœur, naïve, Noël et façade gardent leurs accents.",
    ],
    "zh": [
        "九月份的电费发票已通过银行转账全额支付。",
        "请查收附件中的已签署合同和送货单。",
        "会议定于星期三上午十点在三号会议室举行。",
        "增值税专用发票号码：０１２３４５６７",
    ],
    "ja": [
        "九月分の電気料金の請求書は銀行振込で全額支払われました。",
        "署名済みの契約書と納品書を添付いたします。",
        "カタカナ、ひらがな、漢字が混ざった文章です。",
        "合計金額：１２，３４５円（税込）",
    ],
    "ar": [
        "تم دفع فاتورة الكهرباء لشهر سبتمبر بالكامل عن طريق التحويل المصرفي.",
        "يرجى الاطلاع على العقد الموقع ومذكرة التسليم المرفقة.",
        "المبلغ الإجمالي: ١٢٣٤٫٥٦ درهم",
    ],
    "other": [
        "Привет, это счёт за электричество за сентябрь.",
        "Ελληνικά: τιμολόγιο ηλεκτρικού ρεύματος.",
        "हिन्दी में बिजली का बिल सितंबर के लिए।",
        "한국어 전기 요금 청구서가 도착했습니다.",
        "Tiếng Việt có dấu: hóa đơn tiền điện tháng chín.",
        "עברית: חשבונית חשמל לחודש ספטמבר.",
    ],
}

EMOJI = ["😀", "👍🏽", "👨‍👩‍👧‍👦", "🇪🇸", "🧾", "✅", "❤️", "🤖", "☕", "🚀", "🏳️‍🌈", "🫠"]
SPECIAL = [
    " ", "  ", "\t", "\n", "\r\n", " ", " ", "　", "​", "﻿",
    "\x01", "\x07", "\x1b", "\x7f", "\u0085", " ", "�", "\U0010fffd",
    "é", "é", "Ａ", "ﬁ", "Ⅻ", "½", "<b>", "&amp;", "<mask>", "<bos>", "<eos>", "<pad>",
    "<|endoftext|>", "<|im_start|>", "▁", "Ġ", "##", "</s>",
]
WORDS = sum((s.split() for v in SENTENCES.values() for s in v), [])


def build(ocr_lines=(), count=2100, seed=7):
    """Returns at least count strings; OCR lines come first."""
    rng = random.Random(seed)
    out = []
    seen = set()

    def add(text):
        if text not in seen:
            seen.add(text)
            out.append(text)

    for line in ocr_lines:
        add(line)
    for group in SENTENCES.values():
        for s in group:
            add(s)
            add(s.upper())
            add(s.lower())
            add("  " + s + "  ")
    for e in EMOJI:
        add(e)
        add("Factura " + e + " pagada")
    for s in SPECIAL:
        add(s)
        add("a" + s + "b")
        add(s * 5)
    langs = list(SENTENCES)
    while len(out) < count - 40:
        kind = rng.randrange(8)
        if kind == 0:  # mixed-language sentence
            add(" ".join(rng.choice(SENTENCES[rng.choice(langs)]) for _ in range(rng.randint(2, 4))))
        elif kind == 1:  # random words with punctuation and numbers
            words = [rng.choice(WORDS) for _ in range(rng.randint(1, 20))]
            if rng.random() < 0.5:
                words.insert(rng.randrange(len(words) + 1), str(rng.randint(0, 10**rng.randint(1, 12))))
            add(rng.choice([" ", "  ", "\n", "\t", ", "]).join(words))
        elif kind == 2:  # emoji and whitespace soup
            parts = [rng.choice(EMOJI + SPECIAL + WORDS) for _ in range(rng.randint(1, 12))]
            add("".join(parts))
        elif kind == 3:  # random code points from many blocks
            blocks = [(0x20, 0x7E), (0xA0, 0x24F), (0x370, 0x3FF), (0x400, 0x4FF), (0x590, 0x6FF),
                      (0x900, 0x97F), (0x3040, 0x30FF), (0x4E00, 0x9FFF), (0xAC00, 0xD7A3), (0x1F300, 0x1FAFF)]
            lo, hi = rng.choice(blocks)
            add("".join(chr(rng.randint(lo, hi)) for _ in range(rng.randint(1, 40))))
        elif kind == 4:  # identifiers, URLs, amounts
            add(rng.choice([
                f"https://example.com/invoices/{rng.randint(1, 99999)}?paid=true&amp;lang=es",
                f"IBAN ES{rng.randint(10, 99)} {rng.randint(1000, 9999)} {rng.randint(1000, 9999)} {rng.randint(10**9, 10**10 - 1)}",
                f"{rng.randint(1, 31):02d}/{rng.randint(1, 12):02d}/{rng.randint(1990, 2030)} {rng.randint(0, 23)}:{rng.randint(0, 59):02d}",
                f"{rng.uniform(-1e6, 1e6):.{rng.randint(0, 4)}f}",
                f"user.name+{rng.randint(1, 999)}@mail.example.org",
                "{\"amount\":" + str(rng.randint(0, 2**53)) + ",\"currency\":\"EUR\"}",
            ]))
        elif kind == 5:  # sentence with leading/trailing/mixed whitespace
            s = rng.choice(SENTENCES[rng.choice(langs)])
            add(rng.choice(SPECIAL[:10]) * rng.randint(1, 3) + s + rng.choice(SPECIAL[:10]) * rng.randint(0, 3))
        elif kind == 6:  # repeated tokens
            add(rng.choice(WORDS) * rng.randint(2, 30))
        else:  # case and punctuation variants
            s = rng.choice(SENTENCES[rng.choice(langs)])
            add("".join(c.upper() if rng.random() < 0.3 else c for c in s) + rng.choice(["", "!", "?!", "...", "…"]))
    # very long inputs
    for i in range(40):
        lang = langs[i % len(langs)]
        add(" ".join(rng.choice(SENTENCES[lang]) for _ in range(40 + 10 * i)))
    return out


if __name__ == "__main__":
    print(json.dumps(build(), ensure_ascii=False))

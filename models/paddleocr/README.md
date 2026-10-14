# PaddleOCR models

The model files of the [PaddleOCR engine](../../docs/ocr.md). They are the same files PaperDotNet ships, byte for byte, so both applications read text the same way. Never replace one without the others: the recognizer and the dictionary must match, and the engine checks their class count when it loads.

| File | Bytes | SHA-256 | Source |
| --- | --- | --- | --- |
| `PP-OCRv6_small_det.onnx` | 9,880,512 | `d73e0058b7a8086bbd57f3d10b8bcd4ff95363f67e06e2762b5e814fe9c9410e` | official PaddlePaddle export, [revision `28fe589`](https://huggingface.co/PaddlePaddle/PP-OCRv6_small_det_onnx/resolve/28fe5895c24fd108c19eb3e8479f4ab385fbfc62/inference.onnx) |
| `PP-OCRv6_small_rec.onnx` | 21,159,378 | `5435fd747c9e0efe15a96d0b378d5bd157e9492ed8fd80edf08f30d02fa24634` | official PaddlePaddle export, [revision `b8f84f0`](https://huggingface.co/PaddlePaddle/PP-OCRv6_small_rec_onnx/resolve/b8f84f0b80c529de40b4fbb3544b84fa7233a513/inference.onnx) |
| `ppocrv6_small_dict.txt` | 74,947 | `b5f2bfe2bdd9448429e3e82b51c789775d9b42f2403d082b00662eb77e401c5d` | the 18,708 `PostProcess.character_dict` entries of the recognizer's inference configuration, one per line |
| `ch_PP-LCNet_x0_25_textline_ori_cls_mobile.onnx` | 1,018,508 | `54379ae5174d026780215fc748a7f31910dee36818e63d49e17dc598ecc82df7` | the PaddleOCR text-line orientation classifier as redistributed by RapidOcrNet 4.2.0 |
| `PP-OCRv6_small_det.inference.yml` | | | the detector's upstream inference configuration: BGR input, ImageNet mean and standard deviation, threshold 0.2, unclip ratio 1.4 |

- **Detector:** finds text regions; it does not read characters.
- **Classifier:** turns upside-down lines over.
- **Recognizer:** reads each line. Its classes are the CTC blank, the dictionary entries in order, and a final space. Never reorder the dictionary.

All model weights and the dictionary are Apache-2.0, copyright PaddlePaddle / PaddleOCR contributors; see [LICENSE](LICENSE).

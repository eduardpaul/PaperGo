// The globals the workflow runtime provides besides ECMAScript itself.

declare class TextEncoder {
  encode(input?: string): Uint8Array;
}

declare class TextDecoder {
  decode(input?: ArrayBuffer | ArrayBufferView): string;
}

declare class Buffer extends Uint8Array {
  static from(data: string, encoding: "base64" | "utf8"): Buffer;
  static from(data: ArrayBuffer | Uint8Array): Buffer;
  toString(encoding?: "base64" | "utf8"): string;
}

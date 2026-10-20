// The globals the workflow runtime provides besides ECMAScript itself; the
// SDK adds console.

declare class TextEncoder {
  encode(input?: string): Uint8Array;
}

declare class TextDecoder {
  decode(input?: ArrayBuffer | ArrayBufferView): string;
}

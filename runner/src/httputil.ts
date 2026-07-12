// Small HTTP helpers shared by the server.

import type { IncomingMessage } from 'node:http';

/**
 * B9: distinguishable request-body failure modes so the route layer can map them
 * to the right HTTP status instead of a blanket 500. readBody previously rejected
 * with generic `Error`s, and the top-level handler turned every rejection into a
 * 500 — an oversized upload or malformed JSON (both client faults) looked like a
 * server bug. These typed errors let startServer's catch reply 413 / 400.
 */
export class BodyTooLargeError extends Error {
  constructor(message = 'request body too large') {
    super(message);
    this.name = 'BodyTooLargeError';
  }
}

export class InvalidJsonError extends Error {
  constructor(message = 'invalid JSON body') {
    super(message);
    this.name = 'InvalidJsonError';
  }
}

/** Read and JSON-parse the request body. Returns null on empty body. Rejects with
 * BodyTooLargeError past the 1 MiB cap and InvalidJsonError on unparseable JSON
 * (B9) so callers can map each to 413 / 400 respectively. */
export function readBody<T = unknown>(req: IncomingMessage): Promise<T | null> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    let aborted = false;
    const MAX = 1 << 20; // 1 MiB cap on request bodies.
    req.on('data', (chunk: Buffer) => {
      // §10: once over the cap, discard the rest and let the socket keep
      // draining. We must NOT `req.destroy()` here: the route's catch maps
      // BodyTooLargeError to a 413 on a later microtask, so a synchronous
      // destroy tore the shared request/response socket down first and the
      // client saw ECONNRESET instead of the mapped 413. Leaving the socket up
      // lets that 413 flush; remaining inbound bytes are dropped (no further
      // buffering) so the request still reaches 'end' and the connection frees.
      if (aborted) return;
      size += chunk.length;
      if (size > MAX) {
        aborted = true;
        reject(new BodyTooLargeError());
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => {
      if (aborted) return; // already rejected with BodyTooLargeError
      const raw = Buffer.concat(chunks).toString('utf8');
      if (!raw) return resolve(null);
      try {
        resolve(JSON.parse(raw) as T);
      } catch {
        reject(new InvalidJsonError());
      }
    });
    req.on('error', reject);
  });
}

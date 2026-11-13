import { readFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
const html = readFileSync('dist/index.html', 'utf8');
const source = /<script[^>]+src="([^"]+)"/.exec(html)?.[1];
if (!source?.startsWith('/assets/')) throw new Error('Missing production entry');
const initialAssets = new Set([
  source,
  ...Array.from(
    html.matchAll(/<link\b[^>]*rel="modulepreload"[^>]*href="([^"]+)"/g),
    (match) => match[1],
  ),
]);
const size = [...initialAssets].reduce(
  (total, asset) => total + gzipSync(readFileSync(`dist${asset}`)).length,
  0,
);
const budget = 160 * 1024;
console.log(
  `Initial JavaScript: ${(size / 1024).toFixed(1)} KiB gzip; budget ${budget / 1024} KiB.`,
);
if (size > budget) process.exitCode = 1;

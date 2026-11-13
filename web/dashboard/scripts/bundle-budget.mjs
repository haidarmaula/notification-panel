import { readFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
const html = readFileSync('dist/index.html', 'utf8');
const source = /<script[^>]+src="([^"]+)"/.exec(html)?.[1];
if (!source?.startsWith('/assets/')) throw new Error('Missing production entry');
const size = gzipSync(readFileSync(`dist${source}`)).length;
const budget = 160 * 1024;
console.log(
  `Initial JavaScript: ${(size / 1024).toFixed(1)} KiB gzip; budget ${budget / 1024} KiB.`,
);
if (size > budget) process.exitCode = 1;

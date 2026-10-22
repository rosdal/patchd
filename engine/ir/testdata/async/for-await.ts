async function f(stream: AsyncIterable<Uint8Array>) {
  let n = 0;
  for await (const chunk of stream) n += chunk.length;
  return n;
}

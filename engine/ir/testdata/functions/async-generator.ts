async function* lines(src: AsyncIterable<string>) {
  for await (const chunk of src) yield chunk.trim();
}

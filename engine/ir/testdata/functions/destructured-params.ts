function handler({ body, query: { id } }: any, [first, second]: number[]) {
  return [body, id, first, second];
}

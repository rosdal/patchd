export function handler({ params, body }: { params: any; body: any }) {
  return params.id + body.name;
}

export function f(u?: { profile?: { email: string } }) {
  return u?.profile?.email;
}

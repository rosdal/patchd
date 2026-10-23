function f(user: any) {
  if (user && user.admin) {
    return true;
  }
  return false;
}

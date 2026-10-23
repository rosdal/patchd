function lazy() {
  const fs = require("fs");
  return fs.readFileSync("/etc/passwd");
}
module.exports = { lazy };

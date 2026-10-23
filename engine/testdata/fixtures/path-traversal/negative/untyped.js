// Plain JavaScript with safe file access.
const fs = require("fs");
const path = require("path");

module.exports.show = function (req, res) {
  res.sendFile(path.join(__dirname, "files", path.basename(req.params.name))); // expect-none
};

module.exports.log = function (req, res) {
  fs.appendFile("access.log", req.url + "\n", function () { // expect-none
    res.end();
  });
};

function helper(file) {
  return fs.readFileSync(file); // expect-none
}
module.exports.helper = helper;

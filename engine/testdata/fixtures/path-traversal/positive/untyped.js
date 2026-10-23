// Plain JavaScript handlers reading files.
const fs = require("fs");
const path = require("path");

module.exports.show = function (req, res) {
  res.sendFile(__dirname + "/files/" + req.params.name); // expect: ts/path-traversal
};

module.exports.read = function (req, res) {
  fs.readFile(req.query.p, function (err, data) { // expect: ts/path-traversal
    res.send(data);
  });
};

exports.remove = (req, res) => {
  fs.unlink(path.join("/uploads", req.body.file), () => res.end()); // expect: ts/path-traversal
};

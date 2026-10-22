import express from "express";
const app = express();
app.listen(3000);
export const local = (n: number) => n + 1;
local(2);

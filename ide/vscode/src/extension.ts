import type { ExtensionContext } from "vscode";
import { LanguageClient, type ServerOptions } from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export async function activate(context: ExtensionContext): Promise<void> {
  const serverOptions: ServerOptions = { command: "patchd", args: ["lsp"] };
  client = new LanguageClient("patchd", "Patchd", serverOptions, {
    documentSelector: [
      { scheme: "file", language: "typescript" },
      { scheme: "file", language: "typescriptreact" },
    ],
  });
  context.subscriptions.push(client);
  await client.start();
}

export async function deactivate(): Promise<void> {
  await client?.stop();
}

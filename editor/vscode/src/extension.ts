import { workspace, window, ExtensionContext } from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export function activate(_context: ExtensionContext): void {
  const config = workspace.getConfiguration("cgpipe");
  const serverPath = config.get<string>("serverPath") || "cgp";

  const serverOptions: ServerOptions = {
    command: serverPath,
    args: ["lsp"],
    transport: TransportKind.stdio,
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "cgpipe" }],
    outputChannelName: "cgpipe",
  };

  client = new LanguageClient(
    "cgpipe",
    "cgpipe Language Server",
    serverOptions,
    clientOptions,
  );

  // Start the server. Syntax highlighting comes from the TextMate grammar and
  // works whether or not the server starts, so a missing/old binary degrades
  // gracefully to "highlighting only" with a hint rather than a hard failure.
  client.start().catch((err) => {
    window.showWarningMessage(
      `cgpipe: could not start the language server ("${serverPath} lsp"). ` +
        `Syntax highlighting still works; diagnostics/hover/completion are disabled. ` +
        `Set "cgpipe.serverPath" to your cgp binary. (${err})`,
    );
  });
}

export function deactivate(): Thenable<void> | undefined {
  return client?.stop();
}

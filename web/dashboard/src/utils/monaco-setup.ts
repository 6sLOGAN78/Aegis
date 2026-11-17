/// <reference types="vite/client" />
// Bundle Monaco locally so the editor never fetches from a CDN (CSP blocks cdn.jsdelivr.net).
// Rego uses a custom Monarch tokenizer, so only the base editor worker is required.
import * as monaco from './monaco-core';
import { loader } from '@monaco-editor/react';
import EditorWorker from 'monaco-editor/editor/editor.worker?worker';

self.MonacoEnvironment = {
  getWorker: () => new EditorWorker(),
};

loader.config({ monaco });

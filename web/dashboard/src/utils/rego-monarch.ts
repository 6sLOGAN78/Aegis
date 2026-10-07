import type { languages } from 'monaco-editor';

export const regoLanguageId = 'rego';

export const regoLanguageConfiguration: languages.LanguageConfiguration = {
  comments: {
    lineComment: '#',
  },
  brackets: [
    ['{', '}'],
    ['[', ']'],
    ['(', ')'],
  ],
  autoClosingPairs: [
    { open: '{', close: '}' },
    { open: '[', close: ']' },
    { open: '(', close: ')' },
    { open: '"', close: '"' },
    { open: '`', close: '`' },
  ],
  surroundingPairs: [
    { open: '{', close: '}' },
    { open: '[', close: ']' },
    { open: '(', close: ')' },
    { open: '"', close: '"' },
    { open: '`', close: '`' },
  ],
};

export const regoLanguageDefinition: languages.IMonarchLanguage = {
  keywords: [
    'package', 'import', 'default', 'allow', 'deny',
    'if', 'in', 'contains', 'some', 'every', 'with',
    'as', 'not', 'true', 'false', 'null',
  ],
  typeKeywords: [
    'boolean', 'string', 'number', 'array', 'object', 'set',
  ],
  operators: [
    '=', ':=', '==', '!=', '>=', '<=', '>', '<',
    '+', '-', '*', '/', '%', '&', '|',
  ],
  symbols: /[=><!~?:&|+\-*\/\^%]+/,
  tokenizer: {
    root: [
      [/#.*$/, 'comment'],
      [/"([^"\\]|\\.)*"/, 'string'],
      [/`([^`])*`/, 'string.raw'],
      [/\b(true|false|null)\b/, 'keyword'],
      [/[a-zA-Z_]\w*/, {
        cases: {
          '@keywords': 'keyword',
          '@typeKeywords': 'type',
          '@default': 'identifier',
        },
      }],
      [/\d+(\.\d+)?/, 'number'],
      [/@symbols/, {
        cases: {
          '@operators': 'operator',
          '@default': '',
        },
      }],
      [/[{}()\[\]]/, '@brackets'],
    ],
  },
};

export function registerRegoLanguage(monaco: typeof import('monaco-editor')) {
  const registeredLanguages = monaco.languages.getLanguages();
  if (!registeredLanguages.some((lang) => lang.id === regoLanguageId)) {
    monaco.languages.register({ id: regoLanguageId });
    monaco.languages.setMonarchTokensProvider(regoLanguageId, regoLanguageDefinition);
    monaco.languages.setLanguageConfiguration(regoLanguageId, regoLanguageConfiguration);
  }
}

#!/usr/bin/env python3
"""CommonMark 差分テスト用コーパス（入力のみ）を外部のテスト資産から生成する。

生成したコーパスは tools/gen-commonmark-fixtures.rb で Redmine 本体に通し、
正解データ（internal/textformat/commonmark/testdata/fixtures.json）にする。

使い方:
  mkcorpus.py spec   <goldmark のモジュールディレクトリ> > corpus_spec.json
      CommonMark 仕様の例（goldmark の _test/spec.json）と goldmark 拡張のテスト入力
  mkcorpus.py comrak <comrak クレートの src/tests ディレクトリ> > corpus_comrak.json
      comrak の拡張機能テストに含まれる文字列リテラル（入力と期待 HTML の両方）
  mkcorpus.py rouge  <rouge gem の lib/rouge/demos ディレクトリ> > corpus_highlight.json
      Rouge の各レキサーのデモ（highlight / highlight_file モード）
"""
import json
import os
import re
import sys


def spec(base):
    out = []
    for ex in json.load(open(os.path.join(base, '_test/spec.json'))):
        sec = re.sub(r'[^a-z0-9]+', '-', ex['section'].lower()).strip('-')
        out.append({'name': 'spec-%03d-%s' % (ex['example'], sec), 'input': ex['markdown']})
    for f in ['linkify', 'table', 'tasklist', 'strikethrough', 'footnote']:
        txt = open(os.path.join(base, 'extension/_test/%s.txt' % f)).read()
        for blk in txt.split('//= = = = = = = = = = = = = = = = = = = = = = = =//'):
            parts = blk.split('//- - - - - - - - -//')
            if len(parts) < 3 or 'OPTIONS' in parts[0]:
                continue
            head = parts[0].strip().splitlines()
            num = head[0].split(':')[0].strip() if head else '0'
            md = parts[1][1:] if parts[1].startswith('\n') else parts[1]
            out.append({'name': 'gm-%s-%s' % (f, re.sub(r'[^0-9a-z]+', '', num.lower())), 'input': md})
    return out


def _parse_str(s, i):
    i += 1
    out = []
    while i < len(s):
        c = s[i]
        if c == '"':
            return ''.join(out), i + 1
        if c == '\\':
            n = s[i + 1]
            simple = {'n': '\n', 't': '\t', 'r': '\r', '0': '\0', '\\': '\\', '"': '"', "'": "'"}
            if n in simple:
                out.append(simple[n])
                i += 2
            elif n == 'u':
                j = s.index('}', i)
                out.append(chr(int(s[i + 3:j], 16)))
                i = j + 1
            elif n == '\n':
                i += 2
                while s[i] in ' \t\n':
                    i += 1
            else:
                out.append('\\' + n)
                i += 2
        else:
            out.append(c)
            i += 1
    raise ValueError('unterminated string')


def _parse_raw(s, i):
    j = i + 1
    hashes = 0
    while s[j] == '#':
        hashes += 1
        j += 1
    end = '"' + '#' * hashes
    k = s.index(end, j + 1)
    return s[j + 1:k], k + len(end)


def _literals(src):
    res = []
    i = 0
    while i < len(src):
        if src.startswith('//', i):
            nl = src.find('\n', i)
            i = len(src) if nl < 0 else nl
            continue
        if src.startswith('concat!(', i):
            i += len('concat!(')
            parts = []
            while True:
                while src[i] in ' \t\n,':
                    i += 1
                if src[i] == ')':
                    i += 1
                    break
                if src[i] == '"':
                    v, i = _parse_str(src, i)
                elif src[i] == 'r' and src[i + 1] in '"#':
                    v, i = _parse_raw(src, i)
                else:
                    break
                parts.append(v)
            res.append(''.join(parts))
            continue
        if src[i] == '"' and (i == 0 or not src[i - 1].isalnum()):
            try:
                v, i = _parse_str(src, i)
                res.append(v)
            except Exception:
                i += 1
            continue
        if src[i] == 'r' and i + 1 < len(src) and src[i + 1] in '"#' and (i == 0 or not src[i - 1].isalnum()):
            try:
                v, i = _parse_raw(src, i)
                res.append(v)
            except Exception:
                i += 1
            continue
        i += 1
    return res


def comrak(base):
    out = []
    seen = set()
    for f in ['autolink', 'table', 'footnotes', 'tasklist', 'alerts', 'strikethrough', 'tagfilter',
              'escaped_char_spans', 'regressions', 'core']:
        n = 0
        for lit in _literals(open(os.path.join(base, f + '.rs')).read()):
            if len(lit) < 3 or lit in seen or re.fullmatch(r'[a-z_.]+', lit):
                continue
            seen.add(lit)
            n += 1
            out.append({'name': 'comrak-%s-%03d' % (f, n), 'input': lit})
    return out


def rouge(base):
    out = []
    for tag in sorted(os.listdir(base)):
        src = open(os.path.join(base, tag), encoding='utf-8').read()
        out.append({'name': 'rouge-demo-' + tag, 'mode': 'highlight', 'lang': tag, 'input': src})
        out.append({'name': 'rouge-demo-file-' + tag, 'mode': 'highlight_file', 'filename': 'demo.' + tag,
                    'input': src})
    return out


if __name__ == '__main__':
    if len(sys.argv) != 3 or sys.argv[1] not in ('spec', 'comrak', 'rouge'):
        sys.exit(__doc__)
    data = {'spec': spec, 'comrak': comrak, 'rouge': rouge}[sys.argv[1]](sys.argv[2])
    json.dump(data, sys.stdout, ensure_ascii=False, indent=1)

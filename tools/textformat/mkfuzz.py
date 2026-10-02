#!/usr/bin/env python3
"""構文要素をランダムに組み合わせた CommonMark 差分テスト用コーパスを生成する（乱数シード固定）。

使い方: mkfuzz.py [件数 [シード [名前の接頭辞]]] > corpus_fuzz.json
"""
import json
import random
import sys

INLINE = [
    'text', 'word', '日本語', 'テスト', 'café', ' ', '  ', '*', '**', '_', '__', '~', '~~', '`', '``',
    '[', ']', '(', ')', '!', '<', '>', '&', '&amp;', '&copy;', '&#x41;', '\\*', '\\_', '\\[', '\\\\', '\\',
    'http://example.com', 'https://example.com/a_b?c=d&e=f', 'www.example.com', 'user@example.com',
    'mailto:a@b.cc', '<b>', '</b>', '<span style="color:red">', '</span>', '<br>', '<img src="a.png">',
    '[link](http://x.y)', '![img](a.png "t")', '[ref]', '[^1]', '{{macro}}', '#123', 'r42', '[[Wiki]]',
    '@user', 'user:foo', ':', '.', ',', ';', '?', '"', "'", '|', '\t', '<!-- c -->', '<script>', '</script>',
    '*em*', '**strong**', '~~del~~', '`code`', '<kbd>K</kbd>', '<a href="javascript:x">j</a>', 'x@2x.png',
]

BLOCK_PREFIX = ['', '', '', '# ', '## ', '> ', '- ', '* ', '1. ', '2) ', '    ', '- [ ] ', '- [x] ',
                '> [!NOTE]\n> ', '| ', '```\n', '~~~ruby\n', '<div>\n', '[^1]: ', '[ref]: ']


def inline(r):
    return ''.join(r.choice(INLINE) for _ in range(r.randint(1, 8)))


def block(r):
    p = r.choice(BLOCK_PREFIX)
    if p == '| ':
        n = r.randint(1, 3)
        head = '| ' + ' | '.join(inline(r).replace('\n', ' ') for _ in range(n)) + ' |\n'
        delim = '|' + '|'.join(r.choice(['---', ':--', '--:', ':-:']) for _ in range(n)) + '|\n'
        rows = ''.join('| ' + ' | '.join(inline(r).replace('\n', ' ') for _ in range(r.randint(1, n + 1))) + ' |\n'
                       for _ in range(r.randint(0, 2)))
        return head + delim + rows
    if p.startswith('```') or p.startswith('~~~'):
        fence = p[:3]
        return p + inline(r) + '\n' + inline(r) + '\n' + fence + '\n'
    if p == '<div>\n':
        return p + inline(r) + '\n</div>\n'
    lines = [p + inline(r)]
    for _ in range(r.randint(0, 2)):
        lines.append(r.choice(['', '  ', '> ', '   ']) + inline(r))
    return '\n'.join(lines) + '\n'


def doc(r):
    parts = []
    for _ in range(r.randint(1, 5)):
        parts.append(block(r))
        parts.append(r.choice(['', '\n']))
    return ''.join(parts)


if __name__ == '__main__':
    n = int(sys.argv[1]) if len(sys.argv) > 1 else 400
    seed = int(sys.argv[2]) if len(sys.argv) > 2 else 20261002
    prefix = sys.argv[3] if len(sys.argv) > 3 else "fuzz"
    r = random.Random(seed)
    out = []
    for i in range(n):
        e = {'name': '%s-%04d' % (prefix, i), 'input': doc(r)}
        if i % 10 == 9:
            e['hardbreaks'] = False
        out.append(e)
    json.dump(out, sys.stdout, ensure_ascii=False, indent=1)

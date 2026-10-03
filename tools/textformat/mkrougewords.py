#!/usr/bin/env python3
"""Rouge のレキサー定義から語彙表（Go ソース）を生成する。

使い方:
  mkrougewords.py css     <lexers/css.rb>            > rouge_css_words.go
  mkrougewords.py builtin <lexers/php/keywords.rb> phpBuiltins > rouge_php_builtins.go
  mkrougewords.py builtin <lexers/lua/keywords.rb> luaBuiltins > rouge_lua_builtins.go
  mkrougewords.py apache  <lexers/apache/keywords.rb> > rouge_apache_words.go
"""
import re
import sys

HEADER = '// SPDX-License-Identifier: GPL-2.0-or-later AND MIT\n// Copyright (C) 2026 mikuta0407 and Buropher contributors\n// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),\n// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.\n\n// Code generated from Rouge 4.7 %s by tools/textformat/mkrougewords.py. DO NOT EDIT.\n\npackage highlight\n'


def css(path):
    src = open(path).read()
    out = [HEADER % 'css.rb']
    for name in ['properties', 'builtins', 'colors', 'functions', 'vendor_prefixes']:
        m = re.search(r'def self\.' + name + r'\b.*?%w\((.*?)\)', src, re.S)
        go = 'css' + ''.join(p.capitalize() for p in name.split('_'))
        out.append('var %s = wordset(`%s`)\n' % (go, ' '.join(m.group(1).split())))
    return '\n'.join(out)


def builtin(path, var):
    src = open(path).read()
    words = set()
    for m in re.finditer(r'Set\.new \[(.*?)\]', src, re.S):
        for w in re.findall(r'"((?:[^"\\]|\\.)*)"', m.group(1)):
            words.add(w.replace('\\\\', '\\'))
    out = [HEADER % path.split('lexers/')[-1], 'var %s = map[string]bool{' % var]
    for w in sorted(words):
        out.append('\t"%s": true,' % w.replace('\\', '\\\\'))
    out.append('}\n')
    return '\n'.join(out)


def apache(path):
    src = open(path).read()
    out = [HEADER % 'apache/keywords.rb']
    for name in ['directives', 'sections', 'values']:
        m = re.search(r'def self\.' + name + r'.*?Set\.new \[(.*?)\]', src, re.S)
        words = re.findall(r'"((?:[^"\\]|\\.)*)"', m.group(1))
        out.append('var apache%s = wordset(`%s`)\n' % (name.capitalize(), ' '.join(words)))
    return '\n'.join(out)


if __name__ == '__main__':
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    kind = sys.argv[1]
    if kind == 'css':
        print(css(sys.argv[2]))
    elif kind == 'builtin':
        print(builtin(sys.argv[2], sys.argv[3]))
    elif kind == 'apache':
        print(apache(sys.argv[2]))
    else:
        sys.exit(__doc__)

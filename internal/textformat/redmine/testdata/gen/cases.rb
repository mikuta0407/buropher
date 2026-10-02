# textilizable 差分テストのケース一覧（cases.json）を生成する。
#
#   ruby internal/textformat/redmine/testdata/gen/cases.rb internal/textformat/redmine/testdata/gen/cases.json
#
# 入力の多くは Redmine の test/helpers/application_helper_test.rb と
# test/unit/lib/redmine/wiki_formatting/macros_test.rb から移植したもの。
require 'json'

out = ARGV[0] || abort('usage: cases.rb OUTPUT.json')

$cases = []
$ids = {}

def add(id, text, formatting: 'textile', user: 'anonymous', project: 'ecookbook', object: nil, options: nil)
  raise "duplicate id #{id}" if $ids[id]

  $ids[id] = true
  c = {'id' => id, 'formatting' => formatting, 'user' => user, 'project' => project.to_s}
  c['object'] = object if object
  c['options'] = options if options
  c['text'] = text
  $cases << c
end

FMTS = {'textile' => 'tx', 'common_mark' => 'md'}.freeze

# 同じテキスト群を textile / common_mark の両方で追加する
def both(prefix, texts, **kw)
  texts.each_with_index do |t, i|
    FMTS.each do |fmt, short|
      add("#{prefix}/#{i}-#{short}", t, formatting: fmt, **kw)
    end
  end
end

def textile_only(prefix, texts, **kw)
  texts.each_with_index {|t, i| add("#{prefix}/#{i}", t, formatting: 'textile', **kw)}
end

# ---------------------------------------------------------------- test_redmine_links
redmine_links = [
  '#3, [#3], (#3) and #3.', '#3-14', '#3#note-14', '#03',
  '##3, [##3], (##3) and ##3.', '##3-14', '##3#note-14', '##03',
  'r1', 'r1.', 'r1, r2', 'r1,r2', 'commit:691322a8eb01e11fd7',
  'document#1', 'document:"Test document"',
  'version#2', 'version:1.0', 'version:"1.0"',
  'source:some/file', 'source:/some/file', 'source:/some/file.', 'source:/some/file.ext.',
  'source:/some/file. ', 'source:/some/file.ext. ', 'source:/some/file, ', 'source:/some/file@52',
  'source:/some/file@branch', 'source:/some/file.ext@52', 'source:/some/file#L110',
  'source:/some/file.ext#L110', 'source:/some/file@52#L110',
  'export:/some/file', 'export:/some/file.ext', 'export:/some/file@52', 'export:/some/file.ext@52',
  'export:/some/file@branch',
  'forum#2', 'forum:Discussion', 'message#4', 'message#5',
  'news#1', 'news:"eCookbook first release !"',
  'project#3', 'project:subproject1', 'project:"eCookbook subProject 1"',
  '#0123456789', 'source:', 'http://foo.bar/FAQ#3',
  'user:jsmith', 'user:JSMITH', 'user#2', '@jsmith', '@jsmith.', '@JSMITH', 'user:foobar'
]
both('app/redmine_links', redmine_links)

both('app/note_same_page_issue', ['#note-14'], object: {'type' => 'issue', 'id' => 1})
both('app/note_same_page_journal', ['#note-2'], object: {'type' => 'journal', 'id' => 2})
both('app/not_inside_link', ['r1 should not be parsed in http://example.com/url-r1/'])
both('app/a_without_attrs', ['<a>http://example.com</a>', '<a>#1</a> #1'])

escaped = ['#3.', '#3-14.', '#3#-note14.', 'r1', 'document#1', 'document:"Test document"',
           'version#2', 'version:1.0', 'version:"1.0"', 'source:/some/file']
both('app/escaped', escaped.map {|t| "!#{t}"})

cross = ['document:"Test document"', 'ecookbook:document:"Test document"', 'invalid:document:"Test document"',
         'version:"1.0"', 'ecookbook:version:"1.0"', 'invalid:version:"1.0"',
         'r2', 'ecookbook:r2', 'invalid:r2',
         'source:/some/file', 'ecookbook:source:/some/file', 'invalid:source:/some/file',
         'ecookbook:invalid|r123', 'ecookbook:commit:invalid|abcd', 'invalid:commit:invalid|abcd',
         'ecookbook:source:some/file', 'ecookbook:source:invalid|some/file', 'invalid:source:invalid|some/file',
         'ecookbook:version:1.0 version:2.0', 'ecookbook:#1 ecookbook:##2']
both('app/cross_project', cross, project: 'subproject1')

multi = ['r2', 'invalid|r123', 'commit:invalid|abcd', 'source:some/file', 'source:invalid|some/file']
both('app/multi_repo', multi)

both('app/attachment_links', ['attachment:error281.txt'], options: {'attachments' => [1, 5, 6, 14]})
both('app/attachment_links_issue3', ['attachment:error281.txt', 'attachment:ERROR281.TXT', 'attachment:archive.zip',
                                     'attachment:"error281.txt"', 'attachment:missing.txt'],
     object: {'type' => 'issue', 'id' => 3})

# ---------------------------------------------------------------- auto links / mailto
auto_links = [
  'http://foo.bar', 'http://foo.bar/~user', 'http://foo.bar.', 'https://foo.bar.',
  'This is a link: http://foo.bar.', 'A link (eg. http://foo.bar).', 'http://foo.bar/foo.bar#foo.bar.',
  'http://www.foo.bar/Test_(foobar)', '(see inline link : http://www.foo.bar/Test_(foobar))',
  '(see inline link : http://www.foo.bar/Test)', '(see inline link : http://www.foo.bar/Test).',
  'www.foo.bar', 'http://foo.bar/page?p=1&t=z&s=', 'http://foo.bar/page#125', 'http://foo@www.bar.com',
  'http://foo:bar@www.bar.com', 'ftp://foo.bar', 'ftps://foo.bar', 'sftp://foo.bar',
  'http://example.net/path!602815048C7B5C20!302.html', 'http://foo"bar', '<http://foo.bar>',
  'http://', 'www.', 'test-www.bar.com', 'http://www.redmine.org/example-', 'http://foo.bar/тест',
  'test@foo.bar', 'test@www.foo.bar'
]
both('app/auto_links', auto_links)
textile_only('app/auto_links_textile', [
  '(see "inline link":http://www.foo.bar/Test_(foobar))', '(see "inline link":http://www.foo.bar/Test)',
  '(see "inline link":http://www.foo.bar/Test).'
])

# ---------------------------------------------------------------- images
textile_only('app/inline_images', [
  '!http://foo.bar/image.jpg!', 'floating !>http://foo.bar/image.jpg!',
  'with class !(some-class)http://foo.bar/image.jpg!', 'with class !(wiki-class-foo)http://foo.bar/image.jpg!',
  'with style !{width:100px;height:100px}http://foo.bar/image.jpg!',
  'with title !http://foo.bar/image.jpg(This is a title)!',
  'with title !http://foo.bar/image.jpg(This is a double-quoted "title")!',
  'with query string !http://foo.bar/image.cgi?a=1&b=2!',
  "h1. !foo.png! Heading\n\nCentered image:\n\np=. !bar.gif!\n"
])
all_att = (1..24).to_a
textile_only('app/attached_images', [
  'Inline image: !logo.gif!', 'Inline image: !logo.GIF!', 'Inline WebP image: !logo.webp!',
  'No match: !ogo.gif!', 'No match: !ogo.GIF!', '!logo.gif!:http://foo.bar/',
  '!logo.gif(alt text)!', '!{width:100px}logo.gif(alt text)!', '!logo.gif!', '!testfile.PNG!',
  '!no-match.jpg!', '!no-match.jpg(alt text)!', 'Inline image: !testfile.png!', 'Inline image: !Testfile.PNG!',
  '!testテスト.png!', '!picture.jpg! and !picture.JPG!'
], options: {'attachments' => all_att})
add('app/attached_images_md/0', '![](logo.gif) ![alt](logo.gif "title") ![](testfile.png)', formatting: 'common_mark',
    options: {'attachments' => all_att})
add('app/attached_images_md/1', '<img src="logo.gif" alt="x" width="10"> <img src="logo.webp">', formatting: 'common_mark',
    options: {'attachments' => all_att})
textile_only('app/attached_images_none', ['!logo.gif!', '!logo.gif(alt text)!'], options: {'attachments' => []})
both('app/attached_images_no_inline', ['!logo.gif!'], options: {'attachments' => all_att, 'inline_attachments' => false})
both('app/attached_images_issue14', ['!testfile.png!', '!testテスト.png!', '!private.diff!'],
     user: 'jsmith', project: 'subproject1', object: {'type' => 'issue', 'id' => 14})
both('app/attached_images_journal3', ['!picture.jpg!', 'attachment:picture.jpg', 'attachment:source.rb'],
     object: {'type' => 'journal', 'id' => 3})
both('app/attached_images_wiki4', ['!logo.gif!', 'attachment:logo.gif', '{{thumbnail(logo.gif)}}'],
     object: {'type' => 'wiki_content', 'id' => 4})
both('app/attached_images_wiki1', ['!logo.webp!', 'attachment:ecookbook-gantt.pdf', '!ecookbook-gantt.pdf!'],
     object: {'type' => 'wiki_content', 'id' => 1})
both('app/hires_images', ['!image@2x.png!', '!http://foo.bar/a@3x.jpg!', '!logo@2x.gif!'])

textile_only('app/textile_external_links', [
  'This is a "link":http://foo.bar', 'This is an intern "link":/foo/bar', '"link (Link title)":http://foo.bar',
  '"link (Link title with "double-quotes")":http://foo.bar', "This is not a \"Link\":\n\nAnother paragraph",
  "This is a double quote \"on the first line\nand another on a second line\":test",
  "\"system administrator\":mailto:sysadmin@example.com?subject=redmine%20permissions",
  '"a link":http://example.net/path!602815048C7B5C20!302.html', '"test":http://foo"bar',
  '(see "inline link":http://www.foo.bar/Test-)', 'http://foo.bar/page?p=1&t=z&s=-',
  'This is an intern "link":/foo/bar-', 'This is a "link":http://foo.bar/тест'
])

# ---------------------------------------------------------------- wiki links
wiki_links = [
  '[[CookBook documentation]]', '[[Another page|Page]]', '[[Another page|With _styled_ *title*]]',
  '[[Another page|With title containing <strong>HTML entities &amp; markups</strong>]]',
  '[[CookBook documentation#One-section]]', '[[Another page#anchor|Page]]', '[[Another_page#тест|тест]]',
  '[[#anchor]]', '[[#anchor|One-section]]', '[[Unknown page]]', '[[Unknown page|404]]',
  '[[onlinestore:]]', '[[onlinestore:|Wiki]]', '[[onlinestore:Start page]]', '[[onlinestore:Start page|Text]]',
  '[[onlinestore:Unknown page]]', '-[[Another page|Page]]-', '-[[Another page|Page]] link-',
  '![[Another page|Page]]', '[[unknowproject:Start]]', '[[unknowproject:Start|Page title]]',
  '[[private-child:]]', '[[private-child:Wiki]]', '[[OnlineStore:Start page]]', '[[eCookbook:Another page]]',
  '[[ecookbook:]]', '[[subproject1:Foo]]', '[[Этика менеджмента]]', '[[documentation]]', '[[another page]]',
  '[[Page with sections#Heading 1]]', '[[#Heading 1|go]]', '[[a|b|c]]', "[[multi\nline]]", '[[]]', '[[ ]]'
]
both('app/wiki_links_jsmith', wiki_links, user: 'jsmith')
both('app/wiki_links_anon', wiki_links.first(24))
both('app/wiki_links_admin_os', wiki_links.first(24), user: 'admin', project: 'onlinestore')
both('app/wiki_links_noproject', ['[[CookBook documentation]]', '[[ecookbook:Another page]]', '[[onlinestore:]]'], user: 'admin', project: '')

special = ['[[Jack & Coke]]', '[[a "quoted" name]]', "[[le français, c'est super]]", '[[broken < less]]',
           '[[broken > more]]', '[[[foo]Including [square brackets] in wiki title]]']
both('app/wiki_links_special', special)

local = ['[[CookBook documentation]]', '[[CookBook documentation|documentation]]',
         '[[CookBook documentation#One-section]]', '[[CookBook documentation#One-section|documentation]]',
         '[[Unknown page]]', '[[Unknown page|404]]', '[[Unknown page#anchor]]', '[[Unknown page#anchor|404]]',
         '[[onlinestore:]]', '[[onlinestore:Start page]]']
both('app/wiki_links_local', local, options: {'wiki_links' => 'local'})
both('app/wiki_links_anchor', local, options: {'wiki_links' => 'anchor'})

wikictx = local.first(8) + ['[[Another page]]', '[[Another page|Page]]', '[[Another page#anchor]]',
                            '[[Another page#anchor|Page]]', '[[onlinestore:Unknown]]']
both('app/wiki_links_wiki_ctx', wikictx, object: {'type' => 'wiki_content', 'id' => 2})
both('app/wiki_links_wiki_ctx_jsmith', wikictx, user: 'jsmith', object: {'type' => 'wiki_content', 'id' => 2})
both('app/headings_anchor_mode', ['h1. Some heading', '# Some heading'], object: {'type' => 'wiki_content', 'id' => 2},
     options: {'wiki_links' => 'anchor'})

textile_only('app/wiki_links_in_tables', ["|[[Page|Link title]]|[[Other Page|Other title]]|\n|Cell 21|[[Last page]]|"])
add('app/wiki_links_in_tables_md', "| a | b |\n|---|---|\n| [[Page|Link title]] | [[Other Page]] |", formatting: 'common_mark')

# ---------------------------------------------------------------- html / pre
textile_only('app/html_tags', [
  "<div>content</div>", "<div class=\"bold\">content</div>", "<script>some script;</script>",
  "<pre>\nline 1\nline2</pre>", "<pre><code>\nline 1\nline2</code></pre>",
  "<pre><div class=\"foo\">content</div></pre>", "<pre><div class=\"<foo\">content</div></pre>",
  "<!-- opening comment", "<pre class='foo'>some text</pre>", '<pre class="foo">some text</pre>',
  "<pre class='foo bar'>some text</pre>", '<pre class="foo bar">some text</pre>',
  "<pre onmouseover='alert(1)'>some text</pre>", '<pre><code class=""onmouseover="alert(1)">text</code></pre>',
  '<pre class=""onmouseover="alert(1)">text</pre>', "<pre>preformatted text</pre>",
  "<notextile>no *textile* formatting</notextile>", "<notextile>this is <tag>a tag</tag></notextile>"
])
add('app/html_tags_md/0', "<div>content</div>\n\n<script>x</script>\n\n<pre class='foo'>some text</pre>", formatting: 'common_mark')
add('app/html_tags_md/1', "<pre><code>\n#1 [[Wiki]]\n</code></pre>\n\n#1", formatting: 'common_mark')

pre_texts = [
  "Before\n\n<pre>\n<prepared-statement-cache-size>32</prepared-statement-cache-size>\n</pre>\n\nAfter\n",
  "[[CookBook documentation]]\n\n#1\n\n<pre>\n[[CookBook documentation]]\n\n#1\n</pre>\n",
  "<pre><code>\n", "unbalanced</pre>", "a <code>#1 r1</code> #1", "<code>[[Wiki]]</code> [[Wiki]]",
  "<pre>\n{{hello_world}}\n</pre>\n\n{{hello_world}}", "<pre><code>#1</pre> #1 </code> #2",
  "<PRE>#1</PRE> #1", "@#1@ #1"
]
textile_only('app/pre', pre_texts)
both('app/pre_md', ["```\n#1 [[Wiki]] {{hello_world}}\n```\n\n#1 [[Wiki]]", "`#1` and #1", "    #1 indented\n\n#2",
                    "```ruby\nputs '#1'\n```", "~~~\n<b>#1</b>\n~~~"].map {|t| t})

textile_only('app/syntax', [
  "<pre><code class=\"ECMA_script\">\n/* Hello */\ndocument.write(\"Hello World!\");\n</code></pre>\n",
  "<pre><code class=\"ruby\">\nx = a & b\n</code></pre>\n",
  "<pre><code class=\"unknownlang\">\nx = 1 #1\n</code></pre>",
  "<pre><code class=\"python\">def f(x):\n    return x # r1\n</code></pre>"
])

textile_only('app/text_formatting', [
  '*_+bold, italic and underline+_*', '(_text within parentheses_)', 'a *Humane Web* Text Generator',
  'a H *umane* W *eb* T *ext* G *enerator*', 'a *H* umane *W* eb *T* ext *G* enerator', '---', 'Dashes: ---'
])

# ---------------------------------------------------------------- headings / toc
textile_only('app/headings', ['h1. Some heading', 'h1. Some heading related to version 0.5'])
add('app/headings_md/0', '# Some heading', formatting: 'common_mark')
add('app/headings_md/1', '# Some heading related to version 0.5', formatting: 'common_mark')

toc_raw = <<~RAW
  {{toc}}

  h1. Title

  Lorem ipsum dolor sit amet, consectetuer adipiscing elit. Maecenas sed libero.

  h2. Subtitle with a [[Wiki]] link

  Nullam commodo metus accumsan nulla. Curabitur lobortis dui id dolor.

  h2. Subtitle with [[Wiki|another Wiki]] link

  h2. Subtitle with %{color:red}red text%

  <pre>
  some code
  </pre>

  h3. Subtitle with *some* _modifiers_

  h3. Subtitle with @inline code@

  h1. Another title

  h3. An "Internet link":http://www.redmine.org/ inside subtitle

  h2. "Project Name !/attachments/1234/logo_small.gif! !/attachments/5678/logo_2.png!":/projects/projectname/issues

RAW
add('app/toc/full', toc_raw)
add('app/toc/unique', "{{toc}}\n\nh1. Title\n\nh2. Subtitle\n\nh2. Subtitle\n")
add('app/toc/included', "{{toc}}\n\nh1. Included\n\n{{include(Child_1)}}\n")
add('app/toc/left', "{{<toc}}\n\nh1. Heading")
add('app/toc/right', "{{>toc}}\n\nh1. Heading")
add('app/toc/plain', "{{toc}}\n\nh1. Heading")
add('app/toc_md/plain', "{{toc}}\n\n# Heading", formatting: 'common_mark')
add('app/toc_md/left', "{{<toc}}\n\n# Heading", formatting: 'common_mark')
add('app/toc_md/right', "{{>toc}}\n\n# Heading", formatting: 'common_mark')
add('app/toc_md/full', "{{toc}}\n\n# Title\n\n## Sub [[Wiki]] link\n\n### Deep *em* `code`\n\n##### Five\n\n# Another\n\n## Sub\n\n## Sub\n", formatting: 'common_mark')
add('app/toc/noheadings', "{{toc}}\n\nno headings here")
add('app/toc/deep', "{{toc}}\n\nh3. three\n\nh1. one\n\nh5. five\n\nh2. two\n\nh4. four\n\nh6. six")
add('app/toc/headings_false', "{{toc}}\n\nh1. Heading", options: {'headings' => false})
add('app/toc/inline', "Some text {{toc}}\n\nh1. Heading")
add('app/toc/twice', "{{toc}}\n\nh1. A\n\n{{>toc}}")

sec_raw = <<~RAW
  h1. Title

  Lorem ipsum dolor sit amet, consectetuer adipiscing elit. Maecenas sed libero.

  h2. Subtitle with a [[Wiki]] link

  h2. Subtitle with *some* _modifiers_

  h2. Subtitle with @inline code@

  <pre>
  some code

  h2. heading inside pre

  <h2>html heading inside pre</h2>
  </pre>

  h2. Subtitle after pre tag
RAW
esl = {'edit_section_links' => {'project_id' => '1', 'id' => 'Test'}}
add('app/section_edit/textile', sec_raw, options: esl)
add('app/section_edit/textile_jsmith', sec_raw, user: 'jsmith', options: {'edit_section_links' => {'project_id' => 'ecookbook', 'id' => 'Page_with_sections'}})
md_sec = <<~RAW
  # Wiki

  ## `Foo` Bar

  The heading above generates multiline HTML.
  Don't assume heading tags are always single-line.

  ```
  <h2>
  <code>Foo</code> Bar</h2>
  ```
RAW
add('app/section_edit/md', md_sec, formatting: 'common_mark', options: esl)
add('app/section_edit/md2', "# A\n\n## B\n\n### C\n\nSetext\n------\n\n## B", formatting: 'common_mark', options: esl)
add('app/section_edit/with_toc', "{{toc}}\n\nh1. A\n\nh2. B\n\nh2. B", options: esl)

# ---------------------------------------------------------------- formatters
fmt_texts = ['a *link*: http://www.example.net/', "line1\nline2\n\npara #1 r1 [[Wiki]] <b>x</b> & y",
             "user@example.com and @jsmith", "http://foo.bar/~x?a=1&b=2 www.redmine.org"]
fmt_texts.each_with_index {|t, i| add("app/null_formatter/#{i}", t, formatting: '')}
add('app/formatting_false/0', '*text*', options: {'formatting' => false})
add('app/formatting_false/1', '<b>#1</b> [[CookBook documentation]] {{hello_world}}', options: {'formatting' => false})
add('app/formatting_true/0', '*text*', options: {'formatting' => true})

# ---------------------------------------------------------------- macros
both('macro/hello', ['{{hello_world}}', '{{hello_world(<tag>)}}', '{{hello_world(http://www.redmine.org, #1)}}',
                     '!{{hello_world}}', '!{{hello_world(<tag>)}}', '{{unknown}}', '{{unknown(*test*)}}',
                     '{{unknown(<tag>)}}', '{{hello_world}} {{hello_world(a, b)}}', '*{{hello_world}}*',
                     '{{HELLO_WORLD}}', '{{hello_world()}}', '{{hello_world("a, b", c)}}', '{{hello_world(a""b)}}',
                     '{{macro(2)}} !{{macro(2)}} {{hello_world(foo)}}', '{{hello_world(a)(b)}}'])
both('macro/hello_obj', ['{{hello_world}}'], object: {'type' => 'issue', 'id' => 1})
both('macro/hello_block', ["{{hello_world\nLine of text\n}}\n\n{{hello_world\nAnother line of text\n}}\n",
                           "{{hello_world(a)\nline1\nline2\n}}", "{{hello_world\n}}", "text {{hello_world\nblock\n}} more"])
both('macro/in_pre', ["{{hello_world(foo)}}\n\n<pre>\n{{hello_world(pre)}}\n!{{hello_world(pre)}}\n</pre>\n\n{{hello_world(bar)}}\n",
                      '<pre>{{hello_world(<tag>)}}</pre>'])
both('macro/macro_list', ['{{macro_list}}'])
both('macro/include', ['{{include(Another page)}}', '{{include(ecookbook:Another page)}}', '{{include(ecookbook:)}}',
                       '{{include(unknowidentifier:somepage)}}', '{{include(onlinestore:Start page)}}',
                       '{{include(Unknown)}}', '{{include}}', '{{include(Page with sections)}}',
                       '{{include(private-child:Wiki)}}'])
both('macro/include_noproj', ['{{include(Another page)}}', '{{include(ecookbook:Another page)}}'], project: '')
both('macro/include_objproj', ['{{include(Another page)}}'], project: '', object: {'type' => 'project', 'id' => 1})
both('macro/include_issueproj', ['{{include(Another page)}}'], project: '', object: {'type' => 'issue', 'id' => 1})
both('macro/include_circular', ['{{include(Another page)}}', '{{include(CookBook documentation)}}'],
     object: {'type' => 'wiki_content', 'id' => 2})
both('macro/include_jsmith_os', ['{{include(Start page)}}', '{{include(onlinestore:Start page)}}'], user: 'jsmith', project: 'onlinestore')
add('macro/include_anon_os/0', '{{include(onlinestore:Start page)}}', project: 'onlinestore')
add('macro/include_inline_false/0', '{{include(Page with an inline image)}}', options: {'inline_attachments' => false})

collapse = ["{{collapse\n*Collapsed* block of text\n}}", "{{collapse(Example)\n*Collapsed* block of text\n}}",
            "{{collapse(Show example, Hide example)\n*Collapsed* block of text\n}}",
            %|{{collapse("Click here, to see the example", Hide example)\n*Collapsed* block of text\n}}|,
            "{{toc}}\n\nh1. Title\n\n{{collapse(Show example, Hide example)\nh2. Heading\n}}\"\n",
            "{{collapse\nfirst\n}}\n\n{{collapse\nsecond #1\n}}", "{{collapse}}", "{{collapse(<b>x</b>)\ny\n}}"]
both('macro/collapse', collapse)

cp1 = {'type' => 'wiki_content', 'id' => 2}
both('macro/child_pages', ['{{child_pages}}', '{{child_pages(parent=1)}}', '{{child_pages(depth=1)}}',
                           '{{child_pages(depth=0)}}'], object: cp1)
both('macro/child_pages_other', ['{{child_pages(Another_page)}}', '{{child_pages(Another_page, parent=1)}}',
                                 '{{child_pages(Unknown)}}', '{{child_pages(CookBook_documentation)}}'],
     object: {'type' => 'wiki_content', 'id' => 1})
both('macro/child_pages_os', ['{{child_pages(ecookbook:Another_page)}}', '{{child_pages(ecookbook:Another_page, parent=1)}}',
                              '{{child_pages(Parent_page)}}', '{{child_pages(Parent_page, parent=1)}}'],
     user: 'admin', project: 'onlinestore', object: {'type' => 'wiki_content', 'id' => 1})
both('macro/child_pages_nowiki', ['{{child_pages}}'])
both('macro/child_pages_private', ['{{child_pages(onlinestore:Parent_page)}}'], project: 'ecookbook')

thumb = ['{{thumbnail(testfile.png)}}', '{{thumbnail(testfile.png, size=400)}}',
         '{{thumbnail(testfile.png, title=Cool image)}}', '{{thumbnail(test.png)}}', '{{thumbnail}}',
         '{{thumbnail(testfile.png, size=abc)}}', '{{thumbnail(testfile.png, size=0)}}',
         '{{thumbnail(testテスト.png, size=100, title=A "b")}}']
both('macro/thumbnail', thumb, user: 'jsmith', project: 'subproject1', object: {'type' => 'issue', 'id' => 14})
both('macro/thumbnail_noobj', ['{{thumbnail(logo.gif)}}'])
both('macro/thumbnail_journal', ['{{thumbnail(picture.jpg)}}'], object: {'type' => 'journal', 'id' => 3})

issue_m = ['{{issue(123)}}', '{{issue(1)}}', '{{issue(1, project=true)}}', '{{issue(1, tracker=false)}}',
           '{{issue(1, subject=false, project=true)}}', '{{issue(1, subject=false, tracker=false)}}',
           '{{issue(1, project=yes)}}', '{{issue(4)}}', '{{issue(14)}}', '{{issue(8)}}', '{{issue}}', '{{issue(abc)}}']
both('macro/issue_anon', issue_m)
both('macro/issue_jsmith', issue_m, user: 'jsmith', project: '')
both('macro/issue_admin', issue_m.first(10), user: 'admin', project: 'onlinestore')

recent = ['{{recent_pages}}', '{{recent_pages(days=8000)}}', '{{recent_pages(days=8000, limit=3)}}',
          '{{recent_pages(days=8000, time=true)}}', '{{recent_pages(days=8000, project=onlinestore)}}',
          '{{recent_pages(days=8000, include_subprojects=true)}}', '{{recent_pages(days=8000, project=unknown)}}',
          '{{recent_pages(days=8000, project=private-child)}}', '{{recent_pages(limit=0, days=8000)}}']
both('macro/recent_anon', recent)
both('macro/recent_jsmith', recent, user: 'jsmith')
both('macro/recent_noproj', ['{{recent_pages(days=8000)}}', '{{recent_pages(days=8000, project=ecookbook)}}'], project: '')

# ---------------------------------------------------------------- broad mix
contexts = [
  ['anon-eco', 'anonymous', 'ecookbook'], ['jsmith-eco', 'jsmith', 'ecookbook'], ['jsmith-os', 'jsmith', 'onlinestore'],
  ['admin-none', 'admin', ''], ['dlopper-eco', 'dlopper', 'ecookbook'], ['anon-os', 'anonymous', 'onlinestore'],
  ['admin-pc', 'admin', 'private-child'], ['rhill-sub', 'rhill', 'subproject1']
]
mix = [
  '#1 #2 #3 #4 #5 #6 #7 #8 #9 #10 #11 #12 #13 #14 #999',
  '##4 ##6 ##14 ##8',
  '#4-1 #6#note-4 #14-5',
  'ecookbook:#4 onlinestore:#4 private-child:#6 unknown:#1',
  'version#1 version#3 version#4 version#5 version#6 version#7',
  'version:Alpha version:"Systemwide visible version" version:2.0 onlinestore:version:Alpha subproject1:version:2.0',
  'news#1 news#2 news#3 news:"100,000 downloads for eCookbook" onlinestore:news:"News on a private project"',
  'forum#1 forum#3 forum:Help forum:Discussion onlinestore:forum:Discussion',
  'message#1 message#2 message#3 message#7',
  'document#1 document#2 document#3 document:"An other document" document:"Unknown"',
  'project#1 project#2 project#5 project#6 project:onlinestore project:private-child project:"OnlineStore"',
  'user#1 user#3 user#5 user#6 user#10 user#12 user#999 user:dlopper2 user:admin',
  '@admin @dlopper @dlopper2 @someone @rhill @nobody @jsmith, (@jsmith) [@jsmith]',
  'r1 r11 r12 r0 ecookbook:r3 onlinestore:r1 commit:691322 commit:deadbeef',
  'source:trunk/a.rb@3#L5 export:trunk/a.rb onlinestore:source:x subproject1:source:y',
  'attachment:error281.txt attachment:document.txt',
  "Line start #1\n(#2) [#3] -#1 ,#2 >#3 x#1 #1x #1_ #1/ #1? #1! #1.",
  '<b>#1</b> <a href="/foo">#1 [[Wiki]]</a> #1',
  'Mixed: see #1, r1, [[CookBook documentation]], version:1.0, @jsmith and http://www.redmine.org/ for details.',
  "* item #1\n* item [[Another page]]\n** nested @jsmith",
  "|_. id |_. ref |\n| 1 | #1 |\n| 2 | document#1 |",
  '"#1":http://example.com and "link to issue":/issues/1 #1',
  'news:"eCookbook first release !" news:eCookbook',
  'document:"Test document" ecookbook:document:"Test document" onlinestore:document:"Test document"',
  'forum:"Discussion" message#5',
  'see version:"1.0", and version:2.0.',
  '!#1 !r1 !@jsmith !user:jsmith !version#2 !document#1 !forum#1 !news#1 !project#1 !message#4',
  'user:"jsmith" @"jsmith" user:jsmith@example.net',
  '#1-14 #1#note-1 ##1-2 #note-1 #note-',
]
mix.each_with_index do |t, i|
  contexts.each do |cid, user, proj|
    FMTS.each do |fmt, short|
      next if short == 'md' && t.start_with?('|_.')

      add("mix/#{i}/#{cid}-#{short}", t, formatting: fmt, user: user, project: proj)
    end
  end
end

# オブジェクト別の attachment: リンク
objs = [
  ['issue1', {'type' => 'issue', 'id' => 1}], ['issue2', {'type' => 'issue', 'id' => 2}],
  ['issue3', {'type' => 'issue', 'id' => 3}], ['journal2', {'type' => 'journal', 'id' => 2}],
  ['journal3', {'type' => 'journal', 'id' => 3}], ['wiki1', {'type' => 'wiki_content', 'id' => 1}],
  ['wiki4', {'type' => 'wiki_content', 'id' => 4}], ['news1', {'type' => 'news', 'id' => 1}],
  ['message1', {'type' => 'message', 'id' => 1}], ['document1', {'type' => 'document', 'id' => 1}],
  ['document3', {'type' => 'document', 'id' => 3}], ['version1', {'type' => 'version', 'id' => 1}],
  ['project1', {'type' => 'project', 'id' => 1}]
]
att_text = 'attachment:error281.txt attachment:source.rb attachment:picture.jpg attachment:logo.gif ' \
           'attachment:foo.zip attachment:document.txt attachment:archive.zip attachment:version_file.zip ' \
           'attachment:project_file.zip !picture.jpg! !logo.gif! {{thumbnail(picture.jpg)}}'
objs.each do |name, o|
  FMTS.each do |fmt, short|
    add("obj_attach/#{name}-#{short}", att_text, formatting: fmt, user: 'admin', project: '', object: o)
  end
end

# 見出し
heads_tx = [
  "h1. Title\n\nh2. Title\n\nh2. Title\n\nh3. Title-2",
  "h1. Ünïcödé 日本語 見出し\n\nh2. Ce n'est pas *gras*",
  "h1. Special <>&\"' chars: (a) [b] {c} 1.5\n\nh2.    spaced    words   ",
  "h1. A -- B - C\n\nh2. _under_ score\n\nh3. @code@ and \"link\":http://x.y",
  "h1(cls). With class\n\nh2{color:red}. Styled\n\np. para",
  "{{toc}}\n\nh1. #1 heading with [[Another page]]\n\nh2. @jsmith mention",
  "h1. !logo.gif! image heading\n\nh2. x",
  "h4. four\n\nh5. five\n\nh6. six\n\n{{>toc}}"
]
heads_md = [
  "# Title\n\n## Title\n\n## Title\n\n### Title-2",
  "# Ünïcödé 日本語 見出し\n\n## Ce n'est pas **gras**",
  "# Special <>&\"' chars: (a) [b] {c} 1.5\n\n##    spaced    words   ",
  "Setext H1\n=========\n\nSetext H2\n---------",
  "{{toc}}\n\n# #1 heading with [[Another page]]\n\n## @jsmith mention",
  "# <b>html</b> in heading\n\n<h2 class=\"x\">raw h2</h2>",
  "#### four\n\n##### five\n\n###### six\n\n{{<toc}}",
  "# A\n\n> # quoted heading\n\n- # list heading"
]
heads_tx.each_with_index {|t, i| add("headings/tx/#{i}", t)}
heads_md.each_with_index {|t, i| add("headings/md/#{i}", t, formatting: 'common_mark')}
heads_tx.each_with_index {|t, i| add("headings/tx_noheadings/#{i}", t, options: {'headings' => false})}

# 実際的な段落
realistic = [
  "Fixed in r2 (see #1 and #2).\n\nThe patch from @dlopper was reviewed by user:jsmith.\nRelated: [[Another page#anchor|docs]].",
  "Steps to reproduce:\n\n# Open #3\n# Click *Save*\n# See error\n\nExpected: no error. See attachment:error281.txt",
  "> quoted text #1\n> more\n\nReply with ##2",
  "Release notes for version:1.0:\n\n* news#1\n* document#1\n* forum#2\n\n{{collapse(Details)\n* message#4\n* project#3\n}}",
  "Code:\n\n<pre><code class=\"ruby\">\n# #1 should stay\nputs \"r1\"\n</code></pre>\n\nAfter #1",
  "Contact admin@somenet.foo or visit www.redmine.org/projects/redmine/wiki?x=1&y=2.",
  "Mention at end @jsmith\nMention mid @admin text\nEmail-like @jsmith@somenet.foo",
]
realistic_md = [
  "Fixed in r2 (see #1 and #2).\n\nThe patch from @dlopper was reviewed by user:jsmith.\nRelated: [[Another page#anchor|docs]].",
  "Steps to reproduce:\n\n1. Open #3\n2. Click **Save**\n3. See error\n\nExpected: no error. See attachment:error281.txt",
  "> quoted text #1\n> more\n\nReply with ##2",
  "Release notes for version:1.0:\n\n* news#1\n* document#1\n* forum#2\n\n{{collapse(Details)\n* message#4\n* project#3\n}}",
  "Code:\n\n```ruby\n# #1 should stay\nputs \"r1\"\n```\n\nAfter #1",
  "Contact admin@somenet.foo or visit www.redmine.org/projects/redmine/wiki?x=1&y=2.",
  "Mention at end @jsmith\nMention mid @admin text\nEmail-like @jsmith@somenet.foo",
  "- [ ] task #1\n- [x] done r1\n\n| a | b |\n|---|---|\n| #1 | [[Wiki]] |\n\nFootnote[^1]\n\n[^1]: note #2",
  "> [!NOTE]\n> See #1\n\n~~struck #2~~ <u>under</u>",
  "[link #1](http://example.com/#1) ![img](http://example.com/a.png) <http://example.com/r1>",
]
realistic.each_with_index do |t, i|
  [['anon', 'anonymous'], ['jsmith', 'jsmith']].each do |n, u|
    add("real/tx/#{i}-#{n}", t, user: u, object: {'type' => 'issue', 'id' => 3})
  end
end
realistic_md.each_with_index do |t, i|
  [['anon', 'anonymous'], ['jsmith', 'jsmith']].each do |n, u|
    add("real/md/#{i}-#{n}", t, formatting: 'common_mark', user: u, object: {'type' => 'issue', 'id' => 3})
  end
end

# ---------------------------------------------------------------- extra: 境界ケース
extra = [
  # Wiki リンクのエスケープ・特殊文字
  '[[CookBook documentation#Ünicode heading]]', '[[Ä page]]', '[[Page with spaces/slash]]',
  '[[ecookbook:Another_page#Anchor|Text]]', '[[onlinestore:]]', '[[Unknown:Page]]',
  '[[eCookbook:CookBook documentation]]', '[[ecookbook:|Home]]', '[[#Some anchor|<b>bold</b>]]',
  '[[Page "quoted" & <tag>]]', '[[Этика менеджмента]]', '[[Page#a b c]]', '[[Page_with_sections#Heading-1]]',
  '![[CookBook documentation]]', '[[a|b|c]]', "[[multi\nline]]", '[[ Spaced ]]', '[[page?x=1]]',
  # Redmine リンクの境界
  '#1#note-1', '##1-1', '#note-3', '#note', 'rnote', '#1x', '#1/', '#1_', '#1-', '(#1)', '-#1', ',#1', '>#1',
  'x#1', '#1:', '#1;', '#1!', '#1?', "#1\n#2", 'ecookbook:#1', 'onlinestore:#4', 'unknown:#1',
  'ecookbook:r1', 'onlinestore:r1', 'ecookbook:document:"Test document"', 'ecookbook:version:1.0',
  'onlinestore:version:"Alpha"', 'version#7', 'version:"Systemwide visible version"', 'version#3',
  'source:"some file"', 'source:some/file@BRANCH', 'source:some/file@a b', 'export:"a b"',
  'source:ecookbook|foo', 'commit:691322', 'commit:deadbeef', 'onlinestore:source:x',
  'message#1', 'message#2', 'message#99', 'forum#1', 'forum:"Help"', 'news#2', 'news#3', 'news:Unknown',
  'document#2', 'document#3', 'project#2', 'project#5', 'project:onlinestore', 'project:"Private child of eCookbook"',
  'user#1', 'user#3', 'user#4', 'user#5', 'user#7', 'user#8', 'user:admin', 'user:"jsmith"', '@admin', '@someone',
  '@dlopper2', '@rhill,', '@jsmith!', '@ jsmith', '@', 'attachment:error281.txt', 'attachment:ERROR281.TXT',
  'attachment:"error281.txt"', 'attachment:unknown.txt', '!#1', '!r1', '!document#1', '!@jsmith', '!user:jsmith',
  '<a href="#">#1</a> #1', "<a href=\"#\">\n#1</a>", '<code>#1</code> #1', '<pre>#1</pre> #2',
  # マクロの境界
  '{{hello_world("a, b", c)}}', '{{hello_world(""quoted"")}}', '{{hello_world( x , y )}}', '{{HELLO_WORLD}}',
  "{{hello_world\n\n}}", "{{macro_list(x)\nblock\n}}", '!{{hello_world}}', '{{unknown_macro}}', '{{macro(0)}}',
  '{{issue}}', '{{issue(abc)}}', '{{issue( 3)}}', '{{issue(3abc)}}', '{{issue(1, project=yes)}}',
  '{{thumbnail}}', '{{thumbnail(error281.txt, size=abc)}}', '{{thumbnail(error281.txt, size=0)}}',
  '{{recent_pages(limit=0, days=8000)}}', '{{recent_pages(limit=-1, days=8000)}}', '{{recent_pages(days=abc)}}',
  '{{child_pages(Unknown)}}', '{{child_pages(onlinestore:Start_page)}}', '{{include}}', '{{include(onlinestore:Start_page)}}',
  "<pre>{{hello_world}}</pre>", "{{collapse\n#1 and [[Wiki]]\n}}", "{{collapse(A, B)\n{{hello_world}}\n}}",
  # 見出し
  "h1. Ünïcödé 日本語\n\nh2. A\n\nh2. A-2\n\nh2. A\n\nh3. <notextile>x</notextile>", "h1. [[Wiki]] #1 r1\n\n{{>toc}}",
  "{{toc}}\n\nh5. five\n\nh6. six", "{{toc}}\n\nh2. two\n\nh1. one\n\nh4. four",
]
extra_md = [
  "# Ünïcödé 日本語\n\n## A\n\n## A-2\n\n## A\n\n{{<toc}}", "# [[Wiki]] #1\n\n{{toc}}",
  "Setext\n======\n\nSub\n---\n\n{{toc}}", "| #1 | r1 |\n|---|---|\n| @jsmith | [[Wiki]] |",
  "```\n#1 {{hello_world}}\n```\n\n`#2` #3", "<div>#1</div>\n\n<span>@jsmith</span>",
  "[#1](/issues/1) and [r1](http://x/r1) [[Wiki|x]]", "* #1\n  * r1\n    * @jsmith",
  "Text with trailing #1.\nNext line #2, #3; #4!", "<img src=\"logo.gif\"> ![x](logo.gif)",
]
extra.each_with_index do |t, i|
  [['anon', 'anonymous', 'ecookbook'], ['admin', 'admin', 'onlinestore'], ['jsmith', 'jsmith', '']].each do |n, u, pr|
    add("extra/tx/#{i}-#{n}", t, user: u, project: pr, object: {'type' => 'issue', 'id' => 3})
  end
  add("extra/md/#{i}", t, formatting: 'common_mark', user: 'dlopper', project: 'ecookbook')
end
extra_md.each_with_index do |t, i|
  [['anon', 'anonymous'], ['admin', 'admin']].each do |n, u|
    add("extra/md2/#{i}-#{n}", t, formatting: 'common_mark', user: u, object: {'type' => 'wiki_content', 'id' => 1})
  end
end

# only_path: false
full = ['#1 r1 [[Wiki]] document#1 version#2 news#1 forum#1 message#5 project#1 @jsmith attachment:error281.txt source:/a@52#L1',
        "{{thumbnail(logo.gif)}} {{issue(1)}} !logo.gif!", '[[Another page#x]] commit:691322a8eb01e11fd7']
full.each_with_index do |t, i|
  add("extra/full/#{i}", t, user: 'admin', object: {'type' => 'issue', 'id' => 1}, options: {'only_path' => false})
end
add('extra/full/wiki', "!logo.gif! [[New page]]", user: 'admin', object: {'type' => 'wiki_content', 'id' => 4}, options: {'only_path' => false})

# NullFormatter / formatting false
null = ['a *link*: http://www.example.net/', "line1\nline2\n\npara #1 r1", '<b>x</b> & "y" \'z\' @jsmith',
        "[[Wiki]] {{hello_world}}\n\n\n\nfoo@bar.com", "{{toc}}\n\nh1. Title", 'http://a.b/c?x=1&y=2 www.redmine.org']
null.each_with_index do |t, i|
  add("extra/null/#{i}", t, formatting: '', user: 'jsmith')
  add("extra/noformat/#{i}", t, user: 'jsmith', options: {'formatting' => false})
end

# edit_section_links / wiki_links 組み合わせ
sect_tx = "h1. One\n\ntext\n\nh2. Two [[Wiki]]\n\n<pre>\nh2. no\n</pre>\n\nh3. Three\n\n{{toc}}"
sect_md = "# One\n\ntext\n\n## Two [[Wiki]]\n\n```\n## no\n```\n\n### Three\n\n{{toc}}"
add('extra/sect/tx', sect_tx, user: 'admin', object: {'type' => 'wiki_content', 'id' => 1},
    options: {'edit_section_links' => {'project_id' => 'ecookbook', 'id' => 'CookBook_documentation'}})
add('extra/sect/md', sect_md, formatting: 'common_mark', user: 'admin', object: {'type' => 'wiki_content', 'id' => 1},
    options: {'edit_section_links' => {'project_id' => 'ecookbook', 'id' => 'CookBook_documentation'}})
add('extra/sect/anchor', sect_tx, user: 'admin', object: {'type' => 'wiki_content', 'id' => 1},
    options: {'wiki_links' => 'anchor'})
add('extra/sect/local', sect_md, formatting: 'common_mark', user: 'admin', object: {'type' => 'wiki_content', 'id' => 1},
    options: {'wiki_links' => 'local', 'headings' => false})

# 各オブジェクトの添付・hello_world
objs = [['issue', 2], ['journal', 1], ['journal', 2], ['wiki_content', 2], ['wiki_content', 11], ['news', 1],
        ['message', 1], ['document', 1], ['version', 1], ['project', 1], ['project', 2]]
objs.each do |type, id|
  t = "{{hello_world(x)}} attachment:logo.gif !logo.gif! {{thumbnail(logo.gif)}} [[Child 1]] #note-1"
  add("extra/obj/#{type}-#{id}-tx", t, user: 'admin', project: '', object: {'type' => type, 'id' => id})
  add("extra/obj/#{type}-#{id}-md", t, formatting: 'common_mark', user: 'jsmith', project: '', object: {'type' => type, 'id' => id})
end

File.write(out, JSON.pretty_generate($cases) + "\n")
puts "#{$cases.size} cases"

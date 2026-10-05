# roadie_inputs.json（roadie の期待値生成用の入力）を作る。
import json
import os

cases = {
 "basic": 'Issue <a href="http://localhost:3000/issues/1">#1</a> has been reported by A &amp; B &quot;q&quot; &#39;s&#39;.\n<hr />\n',
 "badge": '<h1>\n  <a href="http://x/issues/1">Bug #1: x</a>\n  <span class="badge badge-status-closed">closed</span>\n</h1>\n',
 "details": '<ul class="details"><li><strong>Author: </strong>John</li>\n<li><strong>Status: </strong>New</li></ul>\n',
 "journal": '<ul class="journal details">\n  <li><strong>Status</strong> changed from <i>New</i> to <i>Assigned</i></li>\n  <li><strong>File</strong> <a href="http://localhost:3000/attachments/1">a b.txt</a> added</li>\n</ul>\n',
 "pre": '<pre><code class="ruby syntaxhl" data-language="ruby"><span class="k">def</span> foo\n  &lt;x&gt;\nend\n</code></pre>\n<p>x <code>inline</code></p>\n',
 "pre_leading_nl": '<pre>\n\nfoo\n</pre>\n',
 "table": '<table>\n<thead><tr><th>a</th><th>b</th></tr></thead>\n<tbody><tr><td>1</td><td style="text-align:right">2</td></tr></tbody>\n</table>\n',
 "textile_table": '<table>\n\t\t<tr>\n\t\t\t<td>a</td>\n\t\t</tr>\n\t</table>\n',
 "blockquote": '<blockquote>\n<p>quoted</p>\n<blockquote><p>nested</p></blockquote>\n</blockquote>\n',
 "checkbox": '<ul class="task-list">\n<li class="task-list-item"><input type="checkbox" class="task-list-item-checkbox" disabled="disabled" checked="checked"> done</li>\n<li><input type="checkbox" disabled> todo</li>\n</ul>\n',
 "img": '<p><img src="/attachments/download/3/picture.jpg" alt="" /> <img src="http://localhost:3000/a.png" alt="x" title="t"> <img src="rel/x.png"></p>\n',
 "unicode": '<p>日本語テキスト &nbsp;x&nbsp;y   é ü “quote” — &copy; &hellip;</p>\n',
 "href_specials": '<a href="http://localhost:3000/projects/ecookbook/wiki/日本語_ページ">wiki</a> <a href="http://x/a b?c=d&amp;e=f g#h i">sp</a> <a href="/rel/path?x=1">rel</a> <a href="#anchor">anc</a> <a href="//cdn/x">sch</a> <a href="mailto:a@b.c">m</a> <a href="http://x/&quot;q&quot;&lt;&gt;{|}^[]">chars</a>\n',
 "attr_quotes": '<span title="a &quot;b&quot; &#39;c&#39; &lt;d&gt; &amp;" class="x">t</span>\n',
 "wiki_anchor": '<h2 id="Section">Section<a href="#Section" class="wiki-anchor">&para;</a></h2>\n<h3>h3</h3>\n',
 "comment": '<!-- c --><p>a</p>\n',
 "markdown_alert": '<div class="markdown-alert markdown-alert-note"><p class="markdown-alert-title"><svg viewBox="0 0 16 16" width="16" height="16"><path d="M0 8z"></path></svg>Note</p><p>Body</p></div>\n',
 "footer": '<hr />\n<span class="footer"><p>You have received this notification.<br />\nTo change: <a class="external" href="http://hostname/my/account">http://hostname/my/account</a></p></span>\n',
 "header": '<span class="header"><p><strong>Header</strong> text</p></span>\n',
 "fieldset": '  <fieldset class="attachments"><legend>Files</legend>\n    <a href="http://localhost:3000/attachments/download/1/error281.txt">error281.txt</a>\n    (28 Bytes)<br />\n  </fieldset>\n',
 "style_attr": '<p style="color:red">x</p><span style="background:url(/images/x.png)">y</span>\n',
 "p_in_span_div": '<span class="footer"><div>div</div><p>p</p></span>\n<p>a<div>b</div>c</p>\n',
 "br_variants": '<p>a<br>b<br/>c<br />d</p>\n',
 "empty_elems": '<p></p><div></div><span></span><a name="x"></a>\n',
 "entities_text": '<p>&lt;script&gt;alert(1)&lt;/script&gt; &amp;amp; 1 &gt; 0 &apos;</p>\n',
 "del_ins": '<p><del>old</del> <ins>new</ins> <em>e</em> <strong>s</strong> <u>u</u> <sup>1</sup></p>\n',
 "li_text": '<ul>\n  <li>eCookbook - <a class="issue tracker-1 status-1" href="http://localhost:3000/issues/3">Bug #3</a>: Error (5 days late)</li>\n</ul>\n',
 "select_bool": '<p><select multiple><option selected value="1">a</option></select><input type="text" readonly value="v"></p>\n',
 "pre_single_nl": '<pre>\nfoo\n</pre>\n<pre><code>\nbar</code></pre>\n',
 "nested_lists": '<ol>\n<li>a\n<ul><li>b</li></ul>\n</li>\n</ol>\n',
 "leading_text": 'text first\n<p>para</p>\ntrailing',
 "pre_copy_button": '<div class="pre-wrapper" data-controller="sticky-copy-button"><a class="copy-pre-content-link icon-only" data-action="click->sticky-copy-button#copy"><svg class="s18 icon-svg" aria-hidden="true"><use href="http://localhost:3000/assets/icons.svg#icon--copy-pre-content"></use></svg><span class="icon-label hidden">Copy</span></a><pre>foo</pre></div>\n',
}
here = os.path.dirname(os.path.abspath(__file__))
json.dump([{"name": k, "body": v} for k, v in cases.items()], open(os.path.join(here, 'roadie_inputs.json'), 'w'), ensure_ascii=False, indent=1)

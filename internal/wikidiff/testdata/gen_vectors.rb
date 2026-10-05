# Redmine の diff 実装から Go テスト用ベクタを生成する
require 'json'
require 'yaml'
require 'cgi'
R = File.expand_path('../../../_reference/redmine', __dir__)
module ActionView; module Helpers; module TagHelper; end; module TextHelper; end; module OutputSafetyHelper; end; end; end
require 'erb'

class SafeBuffer < String
  def html_safe?; true; end
  def html_safe; self; end
  def +(other)
    raise "concat nil" if other.nil?
    o = other.respond_to?(:html_safe?) && other.html_safe? ? other : CGI.escapeHTML(other.to_str)
    SafeBuffer.new(String.new(self) + o)
  end
end
class String
  def html_safe?; false; end
  def html_safe; SafeBuffer.new(self); end
end
class SafeBuffer
  def html_safe?; true; end
end
class NilClass; def html_safe?; false; end; end
def esc(s)
  return s if s.is_a?(SafeBuffer)
  s = s.to_s
  s.html_safe? ? s : SafeBuffer.new(CGI.escapeHTML(s))
end

require R + '/lib/redmine/string_array_diff/diffable'
require R + '/lib/redmine/string_array_diff/diff'
load R + '/lib/redmine/helpers/diff.rb'
class Redmine::Helpers::Diff
  def h(s); esc(s); end
  def safe_join(arr, sep); arr.flatten.map { |i| esc(i) }.join(sep); end
end

# WikiAnnotate を汎用化したもの
Ver = Struct.new(:version, :author, :text, :previous)
eval(File.read(R + '/app/models/wiki_annotate.rb'))

out_diff = []
out_html = []
out_ann = []

rng = Random.new(20240601)
WORDS = %w[a b c foo bar baz <x> & " ' qux a a b] + ["", " ", "  ", "\t", "\n", "\r\n", "\v", "\f", "日本"]
def rand_text(rng, n)
  s = +""
  n.times do
    r = rng.rand(10)
    if r < 6
      s << WORDS[rng.rand(WORDS.size)]
    elsif r < 8
      s << [" ", " ", "  ", "\t", "\n", "\r\n", " \n "][rng.rand(7)]
    else
      s << %w[x y z <b> &amp;][rng.rand(5)]
    end
  end
  s
end
def rand_arr(rng, n, alpha)
  Array.new(n) { alpha[rng.rand(alpha.size)] }
end
def mutate(rng, arr, alpha)
  a = arr.dup
  rng.rand(5).times do
    case rng.rand(3)
    when 0 then a.insert(rng.rand(a.size + 1), alpha[rng.rand(alpha.size)])
    when 1 then a.delete_at(rng.rand(a.size)) unless a.empty?
    else a[rng.rand(a.size)] = alpha[rng.rand(alpha.size)] unless a.empty?
    end
  end
  a
end

# 配列 diff
fixed = [[[], []], [["a"], []], [[], ["a"]], [%w[a b c], %w[a b c]], [%w[a b c], %w[c b a]],
         [%w[a b c d], %w[a x c d]], [%w[a a a], %w[a]], [%w[a], %w[a a a]], [%w[x a b], %w[a b]],
         [%w[a b], %w[b]], [%w[a b c d e], %w[e d c b a]], [%w[a b], %w[a b c d e]], [%w[a b c d e], %w[a b]]]
fixed.each { |a, b| out_diff << {"a" => a, "b" => b, "diffs" => a.diff(b).diffs} }
400.times do |i|
  alpha = %w[a b c d e f g][0, 2 + rng.rand(6)]
  a = rand_arr(rng, rng.rand(14), alpha)
  b = rng.rand(3) == 0 ? rand_arr(rng, rng.rand(14), alpha) : mutate(rng, a, alpha)
  out_diff << {"a" => a, "b" => b, "diffs" => a.diff(b).diffs}
end

# to_html
def html_case(to, from)
  begin
    {"to" => to, "from" => from, "html" => String.new(Redmine::Helpers::Diff.new(to, from).to_html), "words" => to.split(/(\s+)/)}
  rescue => e
    {"to" => to, "from" => from, "error" => e.message, "words" => to.split(/(\s+)/)}
  end
end
fixed_h = [["", ""], ["foo", "bar"], ["<stuff> with html & special chars</danger>", "other stuff <script>alert('foo');</alert>"],
           ["a b c", "a b c"], ["a b c", "a c"], ["a c", "a b c"], [" lead", "lead"], ["trail ", "trail"],
           ["a  b\tc\nd\r\ne", "a b c d e"], ["   ", ""], ["", "x y z"], ["x y z", ""], ["a\vb", "a b"],
           ["one two three four five", "one 2 three 4 five"], ["a b c d e f", "f e d c b a"]]
fixed_h.each { |t, f| out_html << html_case(t, f) }
550.times do
  f = rand_text(rng, rng.rand(16))
  t = if rng.rand(3) == 0
        rand_text(rng, rng.rand(16))
      else
        w = f.split(/(\s+)/)
        alpha = WORDS + %w[x y z]
        mutate(rng, w, alpha).join(rng.rand(2) == 0 ? "" : " ")
      end
  out_html << html_case(t, f)
end

# fixture
fx = YAML.unsafe_load(File.read(R + '/test/fixtures/wiki_content_versions.yml'))
v1 = fx['wiki_content_versions_001']['data']
v2 = fx['wiki_content_versions_002']['data']
v3 = fx['wiki_content_versions_003']['data']
c = html_case(v2, v1); c["name"] = "fixture page1 v2 vs v1"; out_html << c
c = html_case(v3, v2); c["name"] = "fixture page1 v3 vs v2"; out_html << c
c = html_case(v3, v1); c["name"] = "fixture page1 v3 vs v1"; out_html << c

# annotate
def ann_case(vers)
  prev = nil
  objs = vers.map { |v| o = Ver.new(v[0], v[1], v[2], prev); prev = o; o }
  a = WikiAnnotate.new(objs.last)
  {"versions" => vers.map { |v| {"version" => v[0], "author" => v[1], "text" => v[2]} },
   "lines" => a.lines.map { |l| {"version" => l[0], "author" => l[1], "text" => l[2]} }}
end
out_ann << ann_case([[1, 2, v1], [2, 1, v2], [3, 1, v3]])
out_ann << ann_case([[2, 1, v2], [3, 1, v3]])
out_ann << ann_case([[1, nil, v1], [2, 1, v2], [3, nil, v3]])
out_ann << ann_case([[1, 5, ""]])
out_ann << ann_case([[1, 5, "a\r\nb\n\n"], [2, 6, "a\nc\r\nb\n"]])
LINES = ["a", "b", "c", "", "foo", "bar <x>", "a", "  "]
200.times do
  n = 1 + rng.rand(5)
  start = rng.rand(4) == 0 ? 1 + rng.rand(3) : 1
  lines = rand_arr(rng, rng.rand(8), LINES)
  vers = []
  n.times do |i|
    lines = mutate(rng, lines, LINES) if i > 0
    sep = ["\n", "\r\n"][rng.rand(2)]
    text = lines.join(sep) + (rng.rand(3) == 0 ? sep * rng.rand(3) : "")
    author = rng.rand(4) == 0 ? nil : 1 + rng.rand(3)
    vers << [start + i, author, text]
  end
  out_ann << ann_case(vers)
end

D = __dir__ + '/'
File.write(D + 'arraydiff.json', JSON.generate(out_diff))
File.write(D + 'wordhtml.json', JSON.generate(out_html))
File.write(D + 'annotate.json', JSON.generate(out_ann))
puts "errors: #{out_html.count { |h| h['error'] }}"
out_html.select { |h| h['error'] }.first(3).each { |h| p h }
p out_html[2]
p out_html[-3]

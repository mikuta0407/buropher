# Redmine の HtmlParser.to_text を Loofah で直接動かす（Go 実装との比較用）
require 'loofah'
require 'json'

TAGS = {
  'base' => {'br' => {:post => "\n"}, 'style' => ''},
}
TAGS['textile'] = TAGS['base'].merge(
  'b' => {:pre => '*', :post => '*'}, 'strong' => {:pre => '*', :post => '*'},
  'i' => {:pre => '_', :post => '_'}, 'em' => {:pre => '_', :post => '_'},
  'u' => {:pre => '+', :post => '+'}, 'strike' => {:pre => '-', :post => '-'},
  'h1' => {:pre => "\n\nh1. ", :post => "\n\n"}, 'h2' => {:pre => "\n\nh2. ", :post => "\n\n"},
  'th' => {:pre => '*', :post => "*\n"}, 'td' => {:pre => '', :post => "\n"},
  'a' => lambda do |node|
    if node.content.strip != '' && node.attributes.key?('href')
      %| "#{node.content}":#{node.attributes['href'].value} |
    elsif node.attributes.key?('href')
      %| #{node.attributes['href'].value} |
    else
      node.content
    end
  end
)

class WikiTags < ::Loofah::Scrubber
  def initialize(tags_to_text)
    super(:direction => :bottom_up)
    @tags_to_text = tags_to_text || {}
  end

  def scrub(node)
    formatting = @tags_to_text[node.name]
    case formatting
    when Hash
      node.add_next_sibling Nokogiri::XML::Text.new("#{formatting[:pre]}#{node.content}#{formatting[:post]}", node.document)
      node.remove
    when String
      node.add_next_sibling Nokogiri::XML::Text.new(formatting, node.document)
      node.remove
    when Proc
      node.add_next_sibling formatting.call(node)
      node.remove
    else
      CONTINUE
    end
  end
end

def to_text(html, tags)
  html = html.gsub(/[\n\r]/, ' ')
  doc = Loofah.document(html)
  doc.scrub!(WikiTags.new(tags))
  doc.scrub!(:newline_block_elements)
  Loofah.remove_extraneous_whitespace(doc.text(:encode_special_chars => false)).strip.squeeze(' ').gsub(/^ +/, '')
end

input = JSON.parse(STDIN.read)
out = input.map { |h, f| to_text(h, TAGS[f]) }
puts JSON.generate(out)

# mail gem でメールを解析し、MailHandler が使う値を JSON で出力する（Go 実装との比較用）
require 'mail'
require 'json'

def to_utf8(str, encoding)
  return '' if str.nil?
  str = str.b
  return str.force_encoding('UTF-8') if str.empty?
  enc = (encoding.nil? || encoding.to_s.strip.empty?) ? 'UTF-8' : encoding
  if enc.to_s.casecmp('UTF-8') != 0
    str.force_encoding(enc)
    str = str.encode('UTF-8', :invalid => :replace, :undef => :replace, :replace => '?')
  else
    str.force_encoding('UTF-8')
    unless str.valid_encoding?
      str = str.encode('UTF-16LE', :invalid => :replace, :undef => :replace, :replace => '?').encode('UTF-8')
    end
  end
  str
end

def parts_text(parts)
  parts.reject(&:attachment?).map do |p|
    cs = Mail::Utilities.pick_encoding(p.charset).to_s
    to_utf8(p.body.decoded, cs)
  end.join("\r\n")
end

out = {}
ARGV.each do |path|
  m = Mail.new(File.binread(path))
  plain = parts_text(m.all_parts.select { |p| p.mime_type == 'text/plain' })
  plain = parts_text([m]) if plain.strip.empty? && m.all_parts.empty?
  out[File.basename(path)] = {
    'subject' => m.subject.to_s,
    'from' => m.from.to_a.map(&:to_s),
    'to' => m.to.to_a.map(&:to_s),
    'cc' => m.cc.to_a.map(&:to_s),
    'ids' => [m.in_reply_to, m.references].flatten.compact.map(&:to_s),
    'plain' => plain,
    'attachments' => m.attachments.map { |a| [a.filename.to_s, a.mime_type.to_s, a.body.decoded.bytesize] },
  }
rescue => e
  out[File.basename(path)] = {'error' => e.message}
end
puts JSON.generate(out)

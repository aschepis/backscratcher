#!/usr/bin/env ruby
# frozen_string_literal: true

# Matches characters that make up horizontal borders, corners, and intersections.
BORDER_CHARS = /[┌┐└┘├┤┬┴┼╋╠╣╬╞╡╪╫─━═╌╍┄┅┈┉\-=+|│║╗╔╚╝╦╩╤╧ ]/

# At least one of these must appear for the line to be considered a separator.
HORIZ_INDICATOR = /[\-=─━═╌╍┄┅┈┉┼├┤╋╠╣╬╞╡╪╫╦╩╤╧┬┴┌┐└┘+]/

# Vertical bar characters used as column separators (single, double, and ASCII).
COLUMN_SEP = /[│║|]/

def separator_line?(line)
  stripped = line.strip
  return false if stripped.empty?
  # A separator line consists entirely of border characters and has at least
  # one horizontal indicator (so a line of spaces doesn't qualify).
  stripped.gsub(BORDER_CHARS, '').empty? && HORIZ_INDICATOR.match?(stripped)
end

def content_line?(line)
  COLUMN_SEP.match?(line)
end

def extract_cells(line)
  parts = line.split(COLUMN_SEP, -1)
  return [] if parts.length < 2
  # Drop the fragment before the first separator and after the last.
  parts[1..-2].map { |c| c.strip }
end

def merge_row_lines(row_lines)
  num_cols = row_lines.map { |l| extract_cells(l).length }.max || 0
  merged = Array.new(num_cols, '')

  row_lines.each do |line|
    extract_cells(line).each_with_index do |cell, i|
      next if i >= num_cols || cell.empty?
      merged[i] = merged[i].empty? ? cell : "#{merged[i]} #{cell}"
    end
  end

  merged
end

def escape_pipes(str)
  str.gsub('|', '\|')
end

def convert(text)
  rows = []
  current_row_lines = []

  text.each_line do |raw|
    line = raw.chomp
    if separator_line?(line)
      if current_row_lines.any?
        rows << current_row_lines
        current_row_lines = []
      end
    elsif content_line?(line)
      current_row_lines << line
    end
  end
  rows << current_row_lines if current_row_lines.any?

  return '' if rows.empty?

  logical_rows = rows.map { |lines| merge_row_lines(lines) }

  header    = logical_rows[0]
  data_rows = logical_rows[1..]

  output = []
  output << "| #{header.map { |c| escape_pipes(c) }.join(' | ')} |"
  output << "| #{Array.new(header.length, '---').join(' | ')} |"
  data_rows.each do |row|
    output << "| #{row.map { |c| escape_pipes(c) }.join(' | ')} |"
  end

  output.join("\n")
end

input = ARGV[0] ? File.read(ARGV[0]) : $stdin.read
result = convert(input)
puts result unless result.empty?

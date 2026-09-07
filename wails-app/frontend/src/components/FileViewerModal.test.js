import { describe, it, expect } from 'vitest'
import { fileViewerKind } from './FileViewerModal.jsx'

describe('fileViewerKind', () => {
  it('recognizes common image extensions', () => {
    expect(fileViewerKind('photo.png')).toBe('image')
    expect(fileViewerKind('photo.JPG')).toBe('image')
    expect(fileViewerKind('logo.svg')).toBe('image')
  })
  it('recognizes pdf', () => {
    expect(fileViewerKind('cv.pdf')).toBe('pdf')
    expect(fileViewerKind('cv.PDF')).toBe('pdf')
  })
  it('recognizes html/htm as the html viewer', () => {
    expect(fileViewerKind('cover_letter.html')).toBe('html')
    expect(fileViewerKind('cover_letter.htm')).toBe('html')
  })
  it('recognizes text/source extensions', () => {
    expect(fileViewerKind('resume.txt')).toBe('text')
    expect(fileViewerKind('notes.md')).toBe('text')
    expect(fileViewerKind('data.json')).toBe('text')
  })
  it('returns null for unsupported extensions', () => {
    expect(fileViewerKind('spreadsheet.xlsx')).toBeNull()
    expect(fileViewerKind('archive.zip')).toBeNull()
    expect(fileViewerKind('slides.pptx')).toBeNull()
  })
  it('returns null for missing/extensionless filenames', () => {
    expect(fileViewerKind('')).toBeNull()
    expect(fileViewerKind(undefined)).toBeNull()
    expect(fileViewerKind('README')).toBeNull()
  })
})

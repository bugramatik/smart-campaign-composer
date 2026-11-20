import { describe, expect, it } from 'vitest'
import { analyzeSMS, render, segmentBreaks, tokens, worstCaseLength, worstCaseText } from '../sms'

describe('analyzeSMS', () => {
  it('keeps plain text in GSM-7 with one 160-character segment', () => {
    expect(analyzeSMS('a'.repeat(160))).toEqual({ characters: 160, encoding: 'GSM-7', segments: 1 })
    expect(analyzeSMS('a'.repeat(161)).segments).toBe(2)
  })

  it('switches to UCS-2 for Turkish letters and splits at 67', () => {
    const t = 'ş' + 'a'.repeat(70)
    expect(analyzeSMS(t)).toEqual({ characters: 71, encoding: 'UCS-2', segments: 2 })
    expect(analyzeSMS('ş'.repeat(70)).segments).toBe(1)
  })

  it('counts code points, not UTF-16 units', () => {
    expect(analyzeSMS('🎉').characters).toBe(1)
  })
})

describe('templates', () => {
  const max = { first_name: 7, city: 9 }

  it('measures the worst case like the backend', () => {
    expect(worstCaseLength('Merhaba {{first_name}}!', max)).toBe('Merhaba !'.length + 7)
    expect(worstCaseLength('{{ city }}', max)).toBe(9)
    expect(worstCaseLength('{{unknown}}', max)).toBe(0)
  })

  it('builds a worst-case string from a sample value', () => {
    expect(worstCaseText('{{first_name}}', max, { first_name: 'Şule' })).toBe('ŞuleŞul')
  })

  it('renders and tokenizes placeholders', () => {
    expect(render('Selam {{first_name}}, {{city}}', { first_name: 'Ece', city: 'İzmir' })).toBe('Selam Ece, İzmir')
    expect(tokens('a {{x}} b')).toEqual([{ text: 'a ' }, { text: '{{x}}', variable: 'x' }, { text: ' b' }])
  })

  it('marks part boundaries only when the limit spans several parts', () => {
    expect(segmentBreaks({ characters: 0, encoding: 'GSM-7', segments: 1 }, 160)).toEqual([])
    expect(segmentBreaks({ characters: 0, encoding: 'UCS-2', segments: 1 }, 160)).toEqual([67, 134])
  })
})

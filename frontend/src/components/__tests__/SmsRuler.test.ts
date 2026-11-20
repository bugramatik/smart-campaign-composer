import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import SmsRuler from '../SmsRuler.vue'
import MarkedText from '../MarkedText.vue'

describe('SmsRuler', () => {
  it('shows the worst-case length and flags overflow', () => {
    const w = mount(SmsRuler, { props: { text: 'Merhaba {{first_name}}', limit: 160, maxLengths: { first_name: 10 } } })
    expect(w.get('[data-test=length]').text()).toBe('18 / 160')
    expect(w.classes()).not.toContain('over')

    const long = mount(SmsRuler, { props: { text: 'a'.repeat(155) + '{{first_name}}', limit: 160, maxLengths: { first_name: 10 } } })
    expect(long.classes()).toContain('over')
  })

  it('reports UCS-2 and draws part boundaries for Turkish text', () => {
    const w = mount(SmsRuler, { props: { text: 'Kışa özel ' + 'a'.repeat(80), limit: 160, maxLengths: {} } })
    expect(w.get('[data-test=encoding]').text()).toBe('UCS-2')
    expect(w.get('[data-test=segments]').text()).toBe('2 parça')
    expect(w.findAll('.tick')).toHaveLength(2)
  })
})

describe('MarkedText', () => {
  it('marks variables, or fills them with the preview user', () => {
    const raw = mount(MarkedText, { props: { text: 'Selam {{first_name}}!' } })
    expect(raw.get('.var').text()).toBe('{{first_name}}')

    const filled = mount(MarkedText, { props: { text: 'Selam {{first_name}}!', values: { first_name: 'Ece' } } })
    expect(filled.text()).toBe('Selam Ece!')
    expect(filled.get('.filled').text()).toBe('Ece')
  })

  it('never interprets content as HTML', () => {
    const w = mount(MarkedText, { props: { text: '<img src=x onerror=alert(1)>' } })
    expect(w.find('img').exists()).toBe(false)
  })
})

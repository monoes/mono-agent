// @vitest-environment jsdom
import React from 'react'
import { it, expect, vi, afterEach, describe } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { ExperienceSection, EducationSection, ProfileDetailsSection, formatCount, formatJoinDate } from './ProfileDetails.jsx'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key, i18n: { language: 'en-US' } }),
}))

afterEach(() => cleanup())

it('renders each position with its company, dates, place and description', () => {
  const onOpenURL = vi.fn()
  render(
    <ExperienceSection
      onOpenURL={onOpenURL}
      experience={[
        {
          title: 'Chief Technology Officer', company: 'Example Labs', company_url: 'https://www.linkedin.com/company/1111/',
          employment_type: 'Full-time', date_range: 'Mar 2021 - Present', duration: '5 yrs 7 mos',
          location: 'Lisbon, Portugal', location_type: 'Hybrid', description: 'Leads the platform team.', skills: 'Go, SQL',
        },
        { title: 'Barista', company: 'Corner Cafe', date_range: '2012 - 2014' },
      ]}
    />,
  )
  expect(screen.getByText('personProfile.experience')).toBeInTheDocument()
  expect(screen.getByText('Chief Technology Officer')).toBeInTheDocument()
  expect(screen.getByText('Mar 2021 - Present · 5 yrs 7 mos')).toBeInTheDocument()
  expect(screen.getByText('Lisbon, Portugal · Hybrid')).toBeInTheDocument()
  expect(screen.getByText('Leads the platform team.')).toBeInTheDocument()
  expect(screen.getByText('Barista')).toBeInTheDocument()
  // The company opens its LinkedIn page; one without a page is plain text.
  fireEvent.click(screen.getByRole('button', { name: 'Example Labs' }))
  expect(onOpenURL).toHaveBeenCalledWith('https://www.linkedin.com/company/1111/')
  expect(screen.queryByRole('button', { name: 'Corner Cafe' })).toBeNull()
})

it('renders schools with degree and field of study', () => {
  render(
    <EducationSection
      onOpenURL={() => {}}
      education={[{ school: 'Example University', school_url: 'https://www.linkedin.com/school/3333/', degree: 'Master of Science', field_of_study: 'Robotics', date_range: '2010 - 2012', grade: '1st class' }]}
    />,
  )
  expect(screen.getByText('personProfile.education')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Example University' })).toBeInTheDocument()
  expect(screen.getByText('Master of Science · Robotics')).toBeInTheDocument()
  expect(screen.getByText('2010 - 2012')).toBeInTheDocument()
})

it('renders nothing without entries', () => {
  const { container } = render(
    <>
      <ExperienceSection experience={[]} />
      <EducationSection education={null} />
    </>,
  )
  expect(container).toBeEmptyDOMElement()
})

describe('ProfileDetailsSection', () => {
  it('renders an Instagram read: category, pronouns, links, contact, threads and highlights', () => {
    const onOpenURL = vi.fn()
    render(
      <ProfileDetailsSection
        onOpenURL={onOpenURL}
        details={{
          profile_category: 'Non-profit organisation', account_type: 'business', is_private: false,
          pronouns: ['they/them'], threads_handle: 'fake.ada',
          links: [{ url: 'https://example.test/shop', title: 'Shop' }, { url: 'example.test/blog' }],
          contact: { email: 'ada@example.test', phone: '+49 30 1234' },
          highlights: ['Backstage', 'Travel'],
        }}
      />,
    )
    expect(screen.getByText('profileDetails.title')).toBeInTheDocument()
    expect(screen.getByText('Non-profit organisation')).toBeInTheDocument()
    expect(screen.getByText('profileDetails.accountTypes.business')).toBeInTheDocument()
    expect(screen.getByText('profileDetails.public')).toBeInTheDocument()
    expect(screen.getByText('they/them')).toBeInTheDocument()
    expect(screen.getByText('Backstage')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Shop' }))
    expect(onOpenURL).toHaveBeenCalledWith('https://example.test/shop')
    // A link without a title shows its host and opens with a scheme.
    fireEvent.click(screen.getByRole('button', { name: 'example.test' }))
    expect(onOpenURL).toHaveBeenCalledWith('https://example.test/blog')
    fireEvent.click(screen.getByRole('button', { name: '@fake.ada' }))
    expect(onOpenURL).toHaveBeenCalledWith('https://www.threads.net/@fake.ada')
    fireEvent.click(screen.getByRole('button', { name: 'ada@example.test' }))
    expect(onOpenURL).toHaveBeenCalledWith('mailto:ada@example.test')
    fireEvent.click(screen.getByRole('button', { name: '+49 30 1234' }))
    expect(onOpenURL).toHaveBeenCalledWith('tel:+49301234')
  })

  it('renders a TikTok read: likes and friends formatted, language', () => {
    render(<ProfileDetailsSection details={{ likes_count: 9682672, friend_count: 17, language: 'en', is_private: true }} />)
    expect(screen.getByText('profileDetails.likes_count')).toBeInTheDocument()
    expect(screen.getByText(formatCount(9682672, 'en-US'))).toBeInTheDocument()
    expect(screen.getByText('17')).toBeInTheDocument()
    expect(screen.getByText('en')).toBeInTheDocument()
    expect(screen.getByText('profileDetails.private')).toBeInTheDocument()
  })

  it('renders an X read: verification, join and birth date, banner and pinned post', () => {
    const onOpenURL = vi.fn()
    const { container } = render(
      <ProfileDetailsSection
        onOpenURL={onOpenURL}
        details={{
          verification_type: 'government', join_date: '2007-12-19', birth_date: 'October 1, 1958',
          affiliates_count: 85, banner_url: 'https://media.example/banner.jpg',
          pinned_post: { url: 'https://x.com/fake/status/1', text: 'Pinned hello' },
        }}
      />,
    )
    expect(screen.getByText('profileDetails.verificationTypes.government')).toBeInTheDocument()
    expect(screen.getByText(formatJoinDate('2007-12-19', 'en-US'))).toBeInTheDocument()
    expect(screen.getByText('October 1, 1958')).toBeInTheDocument()
    expect(screen.getByText('85')).toBeInTheDocument()
    expect(container.querySelector('img.profile-detail-banner')).toHaveAttribute('src', 'https://media.example/banner.jpg')
    expect(screen.getByText('Pinned hello')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'profileDetails.openPost' }))
    expect(onOpenURL).toHaveBeenCalledWith('https://x.com/fake/status/1')
  })

  it('renders nothing without details', () => {
    for (const details of [undefined, null, {}, { links: [], highlights: [] }]) {
      const { container } = render(<ProfileDetailsSection details={details} />)
      expect(container).toBeEmptyDOMElement()
      cleanup()
    }
  })

  it('formats join dates by precision', () => {
    expect(formatJoinDate('2007-12', 'en-US')).toBe('December 2007')
    expect(formatJoinDate('2007-12-19', 'en-US')).toBe('December 19, 2007')
    expect(formatJoinDate('Joined 2007', 'en-US')).toBe('Joined 2007')
  })
})

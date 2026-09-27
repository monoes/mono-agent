// @vitest-environment jsdom
import React from 'react'
import { it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { ExperienceSection, EducationSection } from './ProfileDetails.jsx'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
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

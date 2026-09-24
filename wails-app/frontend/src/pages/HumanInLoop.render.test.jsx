// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import HumanInLoop from './HumanInLoop.jsx'
import * as WailsApp from '../wailsjs/go/main/App'
import { api } from '../services/api.js'

vi.mock('../wailsjs/go/main/App', () => ({
  GetHILItems: vi.fn(),
  ApproveHIL: vi.fn(),
  RejectHIL: vi.fn(),
  GetDraftPersonMessages: vi.fn(),
  SendDraftPersonMessage: vi.fn(),
  RejectDraftPersonMessage: vi.fn(),
  GetPendingPeopleApprovals: vi.fn(),
  ApprovePendingPerson: vi.fn(),
  RejectPendingPerson: vi.fn(),
}))

vi.mock('../services/api.js', () => ({
  api: {
    getPendingPeopleApprovals: vi.fn(),
    approvePendingPerson: vi.fn(),
    rejectPendingPerson: vi.fn(),
    listOrgDesigns: vi.fn(),
    getOrgQuestions: vi.fn(),
    getOrgApprovals: vi.fn(),
    getOrgGates: vi.fn(),
    answerOrgQuestion: vi.fn(),
    approveOrgAction: vi.fn(),
    denyOrgAction: vi.fn(),
    gateApproveOrgAction: vi.fn(),
    gateRejectOrgAction: vi.fn(),
  },
  notify: vi.fn(),
}))

vi.mock('../lib/usePageVisible.js', () => ({
  usePageVisibleRef: () => ({ current: true }),
  useVisibleCatchUp: () => {},
}))

describe('HumanInLoop unified approvals', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    cleanup()
  })

  it('renders lead approvals and triggers approve & send', async () => {
    WailsApp.GetHILItems.mockResolvedValue([])
    WailsApp.GetDraftPersonMessages.mockResolvedValue([])
    api.getPendingPeopleApprovals.mockResolvedValue([
      {
        id: 'lead-1',
        platform: 'LINKEDIN',
        platform_username: 'yashsagar',
        full_name: 'Yash Sagar Santani',
        job_title: 'Full Stack Engineer @ XcelPros',
        introduction: 'Hi Yash, congrats on launching CompX!',
        created_at: '2026-09-24 18:00:00',
      },
    ])
    api.listOrgDesigns.mockResolvedValue({ items: [] })

    const onCountChange = vi.fn()

    render(
      <HumanInLoop
        embedded
        isOpen={true}
        onClose={() => {}}
        onPendingCountChange={onCountChange}
      />
    )

    await waitFor(() => {
      expect(screen.getByText('Yash Sagar Santani')).toBeInTheDocument()
      expect(screen.getByText('@yashsagar')).toBeInTheDocument()
    })

    expect(screen.getByText('Hi Yash, congrats on launching CompX!')).toBeInTheDocument()
    expect(onCountChange).toHaveBeenCalledWith(1)

    // Click Approve & Send
    const approveSendBtn = screen.getByTitle('Save changes, approve, and trigger outreach dispatch workflow')
    expect(approveSendBtn).toBeInTheDocument()

    api.approvePendingPerson.mockResolvedValue({})
    fireEvent.click(approveSendBtn)

    await waitFor(() => {
      expect(api.approvePendingPerson).toHaveBeenCalledWith(
        'lead-1',
        'Hi Yash, congrats on launching CompX!',
        true
      )
    })
  })
})

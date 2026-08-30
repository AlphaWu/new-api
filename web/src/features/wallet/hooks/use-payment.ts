/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import i18next from 'i18next'
import { useState, useCallback } from 'react'
import { toast } from 'sonner'

import {
  calculateAmount,
  calculateStripeAmount,
  calculateWaffoAmount,
  calculateWaffoPancakeAmount,
  calculateZafuPayAmount,
  requestPayment,
  requestStripePayment,
  requestZafuPayPayment,
  isApiSuccess,
} from '../api'
import {
  isStripePayment,
  isWaffoPayment,
  isWaffoPancakePayment,
  isZafuPayPayment,
  submitPaymentForm,
} from '../lib'
import type {
  AmountRequest,
  AmountResponse,
  PaymentResponse,
  StripePaymentResponse,
  ZafuPayPaymentResponse,
} from '../types'

// ============================================================================
// Payment Hook
// ============================================================================

type AmountCalculator = (request: AmountRequest) => Promise<AmountResponse>

export interface PaymentAmountCalculators {
  regular: AmountCalculator
  stripe: AmountCalculator
  waffo: AmountCalculator
  waffoPancake: AmountCalculator
  zafuPay: AmountCalculator
}

const defaultPaymentAmountCalculators: PaymentAmountCalculators = {
  regular: calculateAmount,
  stripe: calculateStripeAmount,
  waffo: calculateWaffoAmount,
  waffoPancake: calculateWaffoPancakeAmount,
  zafuPay: calculateZafuPayAmount,
}

export async function requestPaymentAmount(
  topupAmount: number,
  paymentType: string,
  calculators: PaymentAmountCalculators = defaultPaymentAmountCalculators
): Promise<number> {
  let calculator = calculators.regular
  if (isStripePayment(paymentType)) {
    calculator = calculators.stripe
  } else if (isWaffoPayment(paymentType)) {
    calculator = calculators.waffo
  } else if (isWaffoPancakePayment(paymentType)) {
    calculator = calculators.waffoPancake
  } else if (isZafuPayPayment(paymentType)) {
    calculator = calculators.zafuPay
  }

  const response = await calculator({ amount: topupAmount })
  if (!isApiSuccess(response) || !response.data) {
    // Backend puts the human-readable reason in `data` (e.g. 充值金额过低)
    const reason =
      typeof response.data === 'string' && response.data
        ? response.data
        : response.message
    throw new Error(reason || 'Failed to calculate payment amount')
  }

  const parsed = Number.parseFloat(response.data)
  if (!Number.isFinite(parsed)) {
    throw new Error('Invalid payment amount')
  }
  return parsed
}

export interface PaymentAmountResult {
  amount: number
  /** Human-readable reason when the amount could not be calculated */
  error: string | null
}

export function usePayment() {
  const [amount, setAmount] = useState<number>(0)
  const [calculating, setCalculating] = useState(false)
  const [processing, setProcessing] = useState(false)

  // Calculate payment amount
  const calculatePaymentAmount = useCallback(
    async (
      topupAmount: number,
      paymentType: string
    ): Promise<PaymentAmountResult> => {
      try {
        setCalculating(true)
        const calculatedAmount = await requestPaymentAmount(
          topupAmount,
          paymentType
        )
        setAmount(calculatedAmount)
        return { amount: calculatedAmount, error: null }
      } catch (error) {
        setAmount(0)
        return {
          amount: 0,
          error:
            error instanceof Error
              ? error.message
              : i18next.t('Payment request failed'),
        }
      } finally {
        setCalculating(false)
      }
    },
    []
  )

  // Process payment
  const processPayment = useCallback(
    async (topupAmount: number, paymentType: string) => {
      try {
        setProcessing(true)

        const isStripe = isStripePayment(paymentType)
        const isZafuPay = isZafuPayPayment(paymentType)
        const amount = Math.floor(topupAmount)

        let response: PaymentResponse | StripePaymentResponse | ZafuPayPaymentResponse
        if (isStripe) {
          response = await requestStripePayment({
            amount,
            payment_method: 'stripe',
          })
        } else if (isZafuPay) {
          response = await requestZafuPayPayment({
            amount,
            payment_method: 'zafu_pay',
          })
        } else {
          response = await requestPayment({
            amount,
            payment_method: paymentType,
          })
        }

        if (!isApiSuccess(response)) {
          toast.error(response.message || i18next.t('Payment request failed'))
          return false
        }

        // Handle Stripe payment
        if (isStripe) {
          const payLink = (
            response.data as { pay_link?: string } | undefined
          )?.pay_link
          if (payLink) {
            window.open(payLink, '_blank')
            toast.success(i18next.t('Redirecting to payment page...'))
            return true
          }
        }

        // Handle Zafu Pay: the campus card is charged synchronously by the
        // backend, so a successful response means the balance is credited
        if (isZafuPay) {
          toast.success(i18next.t('Payment successful'))
          return true
        }

        // Handle non-Stripe payment
        if (!isStripe && response.data) {
          const url = (response as unknown as { url?: string }).url
          if (url) {
            submitPaymentForm(url, response.data)
            toast.success(i18next.t('Redirecting to payment page...'))
            return true
          }
        }

        return false
      } catch {
        toast.error(i18next.t('Payment request failed'))
        return false
      } finally {
        setProcessing(false)
      }
    },
    []
  )

  return {
    amount,
    calculating,
    processing,
    calculatePaymentAmount,
    processPayment,
    setAmount,
  }
}

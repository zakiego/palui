/**
 * A select from a flat list of value and label pairs.
 *
 * Every picker in this dashboard is that shape (projects, models, efforts,
 * statuses), and a select wants several components plus the item labels to
 * show a label instead of the raw value. Writing that out a dozen times is
 * how one of them ends up showing a project id instead of its slug.
 */

import type * as React from "react"

import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { picked } from "@/lib/utils"

export interface Option {
  value: string
  label: string
  // Shown before the label, in the list and in the trigger.
  icon?: React.ReactNode
}

export function SimpleSelect({
  id,
  value,
  options,
  onChange,
  className,
  size,
  disabled,
  "aria-label": ariaLabel,
  "aria-invalid": ariaInvalid,
  "aria-describedby": ariaDescribedBy,
}: {
  id?: string
  value: string
  options: Option[]
  onChange: (value: string) => void
  className?: string
  size?: "sm" | "default"
  disabled?: boolean
  "aria-label"?: string
  "aria-invalid"?: boolean
  "aria-describedby"?: string
}) {
  return (
    <Select
      value={value}
      onValueChange={picked(onChange)}
      disabled={disabled}
      items={options.map((o) => ({ value: o.value, label: o.label }))}
    >
      <SelectTrigger
        id={id}
        className={className}
        size={size}
        aria-label={ariaLabel}
        aria-invalid={ariaInvalid || undefined}
        aria-describedby={ariaDescribedBy}
      >
        <SelectValue>
          {({ value: v, label }) => {
            const o = options.find((x) => x.value === v)
            if (!o?.icon) return label
            return (
              <span className="flex min-w-0 items-center gap-2">
                {o.icon}
                <span className="truncate">{o.label}</span>
              </span>
            )
          }}
        </SelectValue>
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value} label={o.label}>
              {o.icon}
              {o.label}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}

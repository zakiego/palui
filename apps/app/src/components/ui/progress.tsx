import * as React from "react"
import {
  Progress as ProgressPrimitive,
  ProgressIndicator as ProgressIndicatorPrimitive,
  ProgressLabel as ProgressLabelPrimitive,
  ProgressTrack as ProgressTrackPrimitive,
  ProgressValue as ProgressValuePrimitive,
} from "@ui-sdk/react/progress"

import { cnState } from "@/lib/utils"

function Progress({
  className,
  children,
  ...props
}: Omit<React.ComponentProps<typeof ProgressPrimitive>, "children"> & {
  children?: React.ReactNode
}) {
  return (
    <ProgressPrimitive className={cnState("flex flex-wrap gap-3", className)} {...props}>
      {children}
      <ProgressTrack>
        <ProgressIndicator />
      </ProgressTrack>
    </ProgressPrimitive>
  )
}

function ProgressTrack({
  className,
  ...props
}: React.ComponentProps<typeof ProgressTrackPrimitive>) {
  return (
    <ProgressTrackPrimitive
      className={cnState(
        "relative flex h-1 w-full items-center overflow-x-hidden rounded-full bg-muted",
        className
      )}
      {...props}
    />
  )
}

function ProgressIndicator({
  className,
  ...props
}: React.ComponentProps<typeof ProgressIndicatorPrimitive>) {
  return (
    <ProgressIndicatorPrimitive
      className={cnState("h-full bg-primary transition-all", className)}
      {...props}
    />
  )
}

function ProgressLabel({
  className,
  ...props
}: React.ComponentProps<typeof ProgressLabelPrimitive>) {
  return (
    <ProgressLabelPrimitive className={cnState("text-sm font-medium", className)} {...props} />
  )
}

function ProgressValue({
  className,
  ...props
}: React.ComponentProps<typeof ProgressValuePrimitive>) {
  return (
    <ProgressValuePrimitive
      className={cnState("ml-auto text-sm text-muted-foreground tabular-nums", className)}
      {...props}
    />
  )
}

export {
  Progress,
  ProgressTrack,
  ProgressIndicator,
  ProgressLabel,
  ProgressValue,
}

"use client"

import * as React from "react"
import {
  Collapsible as CollapsiblePrimitive,
  CollapsiblePanel,
  CollapsibleTrigger as CollapsibleTriggerPrimitive,
} from "@ui-sdk/react/collapsible"

import { partRender } from "@/components/ui/button"

function Collapsible(props: React.ComponentProps<typeof CollapsiblePrimitive>) {
  return <CollapsiblePrimitive {...props} />
}

function CollapsibleTrigger({
  render,
  ...props
}: React.ComponentProps<typeof CollapsibleTriggerPrimitive>) {
  return <CollapsibleTriggerPrimitive render={partRender(render)} {...props} />
}

function CollapsibleContent(props: React.ComponentProps<typeof CollapsiblePanel>) {
  return <CollapsiblePanel {...props} />
}

export { Collapsible, CollapsibleTrigger, CollapsibleContent }

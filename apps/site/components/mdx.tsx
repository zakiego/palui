import { Step, Steps } from "fumadocs-ui/components/steps"
import { Tab, Tabs } from "fumadocs-ui/components/tabs"
import defaultMdxComponents from "fumadocs-ui/mdx"
import type { MDXComponents } from "mdx/types"
import { Callout } from "@/components/docs/callout"
import {
  ApprovalDiagram,
  ArchitectureDiagram,
  BackupDiagram,
  EdgeRoutingDiagram,
  MessageFlowDiagram,
  ConnectorStatesDiagram,
} from "@/components/diagram/docs-diagrams"

export function getMDXComponents(components?: MDXComponents) {
  return {
    ...defaultMdxComponents,
    ApprovalDiagram,
    ArchitectureDiagram,
    BackupDiagram,
    Callout,
    EdgeRoutingDiagram,
    MessageFlowDiagram,
    ConnectorStatesDiagram,
    Step,
    Steps,
    Tab,
    Tabs,
    ...components,
  } satisfies MDXComponents
}

export const useMDXComponents = getMDXComponents

declare global {
  type MDXProvidedComponents = ReturnType<typeof getMDXComponents>
}

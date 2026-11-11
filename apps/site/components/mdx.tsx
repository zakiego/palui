import { Step, Steps } from "fumadocs-ui/components/steps"
import { Tab, Tabs } from "fumadocs-ui/components/tabs"
import defaultMdxComponents from "fumadocs-ui/mdx"
import type { MDXComponents } from "mdx/types"
import {
  ApprovalDiagram,
  ArchitectureDiagram,
  BackupDiagram,
  EdgeRoutingDiagram,
  MessageFlowDiagram,
  SaklarStatesDiagram,
} from "@/components/diagram/docs-diagrams"

export function getMDXComponents(components?: MDXComponents) {
  return {
    ...defaultMdxComponents,
    ApprovalDiagram,
    ArchitectureDiagram,
    BackupDiagram,
    EdgeRoutingDiagram,
    MessageFlowDiagram,
    SaklarStatesDiagram,
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

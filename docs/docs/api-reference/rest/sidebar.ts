import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/pick-face-replenishment-api",
    },
    {
      type: "category",
      label: "Policies",
      link: {
        type: "doc",
        id: "api-reference/rest/policies",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/list-policies",
          label: "List replenishment policies",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/declare-policy",
          label: "Declare or change a replenishment policy",
          className: "api-method put",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-policy",
          label: "Get a replenishment policy",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/remove-policy",
          label: "Remove a replenishment policy",
          className: "api-method delete",
        },
      ],
    },
    {
      type: "category",
      label: "Pick faces",
      link: {
        type: "doc",
        id: "api-reference/rest/pick-faces",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/list-pick-faces",
          label: "List pick faces and their replenishment status",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Replenishments",
      link: {
        type: "doc",
        id: "api-reference/rest/replenishments",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/request-replenishment",
          label: "Raise a replenishment request by hand",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/list-replenishments",
          label: "List replenishment requests",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-replenishment",
          label: "Get a replenishment request",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-replenishment",
          label: "Cancel an open replenishment request",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Sites",
      link: {
        type: "doc",
        id: "api-reference/rest/sites",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/evaluate-site",
          label: "Re-run detection for a site",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Reports",
      link: {
        type: "doc",
        id: "api-reference/rest/reports",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-replenishment-funnel",
          label: "The replenishment funnel per day",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-reports-freshness",
          label: "How far the analytics projection is behind",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Health",
      link: {
        type: "doc",
        id: "api-reference/rest/health",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/healthz",
          label: "Liveness probe",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/readyz",
          label: "Readiness probe",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;

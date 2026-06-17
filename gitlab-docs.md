\# GitLab Duo Chat completions API



This API is used to generate responses for \[GitLab Duo Chat](../user/gitlab\_duo\_chat/\_index.md):



\- On GitLab.com, this API is for internal use only.

\- On GitLab Self-Managed, you can enable this API \[with a feature flag](../administration/feature\_flags/\_index.md) named `access\_rest\_chat`.



Prerequisites:



\- You must be a \[GitLab team member](https://gitlab.com/groups/gitlab-com/-/group\_members).



\## Generate a Chat response



Generates a response for a GitLab Duo Chat question.



{{< history >}}



\- \[Introduced](https://gitlab.com/gitlab-org/gitlab/-/merge\_requests/133015) in GitLab 16.7 \[with a flag](../administration/feature\_flags/\_index.md) named `access\_rest\_chat`. Disabled by default. This feature is internal-only.

\- `additional\_context` parameter \[added](https://gitlab.com/gitlab-org/gitlab/-/merge\_requests/162650) in GitLab 17.4 \[with a flag](../administration/feature\_flags/\_index.md) named `duo\_additional\_context`. Disabled by default. This feature is internal-only.

\- `additional\_context` parameter \[enabled on GitLab.com and GitLab Self-Managed](https://gitlab.com/gitlab-org/gitlab/-/merge\_requests/181305) in GitLab 17.9.

\- `additional\_context` parameter \[generally available](https://gitlab.com/gitlab-org/gitlab/-/issues/514559) in GitLab 18.0. Feature flag `duo\_additional\_context` removed.



{{< /history >}}



> \[!flag]

> The availability of this feature is controlled by a feature flag. For more information, see the history.



```plaintext

POST /chat/completions

```



> \[!note]

> Requests to this endpoint are proxied to the

> \[AI Gateway](https://gitlab.com/gitlab-org/modelops/applied-ml/code-suggestions/ai-assist/-/blob/main/docs/api.md).



Supported attributes:



| Attribute                | Type            | Required | Description                                                             |

|--------------------------|-----------------|----------|-------------------------------------------------------------------------|

| `content`                | string          | Yes      | Question sent to Chat.                                                  |

| `resource\_type`          | string          | No       | Type of resource that is sent with Chat question.                       |

| `resource\_id`            | string, integer | No       | ID of the resource. Can be a resource ID (integer) or a commit hash (string). |

| `referer\_url`            | string          | No       | Referer URL.                                                            |

| `client\_subscription\_id` | string          | No       | Client Subscription ID.                                                 |

| `with\_clean\_history`     | boolean         | No       | Indicates if history should be reset before and after the request. |

| `project\_id`             | integer         | No       | Project ID. Required if `resource\_type` is a commit.                    |

| `additional\_context`     | array           | No       | An array of additional context items for this chat request. See \[Context attributes](#context-attributes) for a list of parameters this attribute accepts. |



\### Context attributes



The `context` attribute accepts a list of elements with the following attributes:



\- `category` - The category of the context element. Valid values are `file`, `merge\_request`, `issue`, or `snippet`.

\- `id` - The ID of the context element.

\- `content` - The content of the context element. The value depends on the category of the context element.

\- `metadata` - The optional additional metadata for this context element. The value depends on the category of the context element.



Example request:



```shell

curl --request POST \\

&#x20; --header "Authorization: Bearer <YOUR\_ACCESS\_TOKEN>" \\

&#x20; --header "Content-Type: application/json" \\

&#x20; --data '{

&#x20;     "content": "how to define class in ruby",

&#x20;     "additional\_context": \[

&#x20;       {

&#x20;         "category": "file",

&#x20;         "id": "main.rb",

&#x20;         "content": "class Foo\\nend"

&#x20;       }

&#x20;     ]

&#x20;   }' \\

&#x20; --url "https://gitlab.example.com/api/v4/chat/completions"

```



Example response:



```json

"To define class in ruby..."

```






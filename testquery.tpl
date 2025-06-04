{{ define "query" }}
    SELECT
        100                                    {{ Scan.Int.Into "Int" }}
        , 'value'                              {{ Scan.String.Into "String" }}
        , true                                 {{ Scan.Into "Bool" }}
        , '2000-12-31'                         {{ (Scan.String.Time DateOnly).Into "Time" }}
        , '300'                                {{ Scan.Text.Into "Big" }}
        , 'https://example.com/path?query=yes' {{ Scan.Binary.Into "URL" }}
        , 'hello,world'                        {{ (Scan.String.Split ",").Into "Slice" }}
        , '{"hello":"world"}'                  {{ Scan.JSON.Into "JSON" }}
{{ end }}
